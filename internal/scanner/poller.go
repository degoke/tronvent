package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/degoke/tronvent/internal/config"
	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/metrics"
)

// blockBatchSize is the maximum number of blocks fetched in a single
// /wallet/getblockbylimitnext request. TronGrid supports up to 100.
const blockBatchSize = 100

const (
	queueTronAdminRetry = "tron-admin-retry"
	queueTronReconcile  = "tron-reconcile"
	queueWorkerID       = "tronvent"
)

// WebhookPayload is the signed webhook body delivered to subscribers.
type WebhookPayload struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	TxHash               string `json:"txHash"`
	FromAddress          string `json:"fromAddress"`
	ToAddress            string `json:"toAddress"`
	Amount               string `json:"amount"`
	TokenContractAddress string `json:"tokenContractAddress,omitempty"`
	BlockNumber          int64  `json:"blockNumber"`
	BlockTimestamp       int64  `json:"blockTimestamp"`
	Confirmations        int64  `json:"confirmations"`
}

// RawEvent is the in-process matched chain event before outbox persistence.
type RawEvent struct {
	Type                 string
	TxHash               string
	FromAddress          string
	ToAddress            string
	Amount               string
	TokenContractAddress string
	BlockNumber          int64
	BlockTimestamp       int64
	Confirmations        int64
}

func rawToWebhookPayload(id string, ev RawEvent) WebhookPayload {
	return WebhookPayload{
		ID:                   id,
		Type:                 ev.Type,
		TxHash:               ev.TxHash,
		FromAddress:          ev.FromAddress,
		ToAddress:            ev.ToAddress,
		Amount:               ev.Amount,
		TokenContractAddress: ev.TokenContractAddress,
		BlockNumber:          ev.BlockNumber,
		BlockTimestamp:       ev.BlockTimestamp,
		Confirmations:        ev.Confirmations,
	}
}

// tronGridBlock is a partial deserialisation of TronGrid's block response.
type tronGridBlock struct {
	BlockID     string `json:"blockID"`
	BlockHeader struct {
		RawData struct {
			Number    int64 `json:"number"`
			Timestamp int64 `json:"timestamp"`
		} `json:"raw_data"`
	} `json:"block_header"`
	Transactions []struct {
		TxID    string `json:"txID"`
		RawData struct {
			Contract []struct {
				Type      string `json:"type"`
				Parameter struct {
					Value struct {
						Amount       int64  `json:"amount"`        // TRX in sun
						ToAddress    string `json:"to_address"`    // hex encoded
						OwnerAddress string `json:"owner_address"` // hex encoded
					} `json:"value"`
				} `json:"parameter"`
			} `json:"contract"`
		} `json:"raw_data"`
	} `json:"transactions"`
}

// tronGridBlockRangeResp is the response from /wallet/getblockbylimitnext.
type tronGridBlockRangeResp struct {
	Block []tronGridBlock `json:"block"`
}

// tronGridTrc20Event is a single Transfer event from the TronGrid v1 events API.
type tronGridTrc20Event struct {
	TransactionID  string `json:"transaction_id"`
	BlockNumber    int64  `json:"block_number"`
	BlockTimestamp int64  `json:"block_timestamp"`
	Result         struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Value string `json:"value"`
	} `json:"result"`
}

// tronGridTrc20EventsResp is the paginated response from
// /v1/contracts/{address}/events.
type tronGridTrc20EventsResp struct {
	Data []tronGridTrc20Event `json:"data"`
	Meta struct {
		PageSize    int    `json:"page_size"`
		At          int64  `json:"at"`
		Fingerprint string `json:"fingerprint"`
	} `json:"meta"`
	Success bool `json:"success"`
}

// scannerDB is the subset of internaldb.Client methods used by Poller.
// Defined as an interface so tests can inject a stub without a real database.
type scannerDB interface {
	GetScannedBlock(ctx context.Context, scope string) (int64, error)
	SetScannedBlock(ctx context.Context, scope string, blockNum int64) error
	ClaimBlockRangeJobs(ctx context.Context, queue string, workerID string, limit int) ([]internaldb.BlockRangeJob, error)
	ClaimBlockRangeJob(ctx context.Context, queue string, workerID string) (*internaldb.BlockRangeJob, error)
	CompleteJob(ctx context.Context, id string) error
	FailJob(ctx context.Context, id string, cause error, retryAfter time.Duration) error
}

type eventOutbox interface {
	EnqueueWebhookEvent(ctx context.Context, eventType, scope, txHash string, blockNumber, blockTimestamp int64, payload any) (string, error)
}

type contractLister interface {
	List() []string
}

// Poller polls TronGrid for new blocks and enqueues matched events to the Postgres outbox.
type Poller struct {
	cfg        *config.Config
	db         scannerDB
	outbox     eventOutbox
	addresses  AddressSet
	contracts  contractLister
	httpClient *http.Client
	sem        chan struct{}
}

// NewPoller creates a Poller wired to Postgres cursors and the webhook outbox.
func NewPoller(
	cfg *config.Config,
	db *internaldb.Client,
	outbox eventOutbox,
	addresses AddressSet,
	contracts contractLister,
) *Poller {
	concurrency := cfg.FetchConcurrency
	if concurrency <= 0 {
		concurrency = 5
	}
	slog.Info("poller configured", "fetchConcurrency", concurrency)
	return &Poller{
		cfg:        cfg,
		db:         db,
		outbox:     outbox,
		addresses:  addresses,
		contracts:  contracts,
		httpClient: &http.Client{Timeout: time.Duration(cfg.HTTPTimeoutSeconds) * time.Second},
		sem:        make(chan struct{}, concurrency),
	}
}

// Run starts the polling loop. It returns only when ctx is cancelled.
func (p *Poller) Run(ctx context.Context) {
	slog.Info("poller started", "intervalMs", p.cfg.PollIntervalMs)

	ticker := time.NewTicker(time.Duration(p.cfg.PollIntervalMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("poller stopping")
			return
		case <-ticker.C:
			if err := p.poll(ctx); err != nil {
				slog.Error("poll error", "err", err)
			}
		}
	}
}

// RunReconciler runs in a background goroutine, consuming block-range backfill
// jobs from the reconcile queue and replaying them via replayScan. It returns
// only when ctx is cancelled.
func (p *Poller) RunReconciler(ctx context.Context) {
	slog.Info("[RECONCILE] reconciler started")

	for {
		jobs, err := p.db.ClaimBlockRangeJobs(ctx, queueTronReconcile, queueWorkerID, 1)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("[RECONCILE] claim error", "err", err)
			time.Sleep(5 * time.Second)
			continue
		}
		if len(jobs) == 0 {
			slog.Debug("[RECONCILE] waiting for work")
			time.Sleep(5 * time.Second)
			continue
		}
		job := jobs[0]

		slog.Info("[RECONCILE] replay start", "from", job.FromBlock, "to", job.ToBlock, "blocks", job.ToBlock-job.FromBlock+1)
		start := time.Now()
		if err := p.replayScan(ctx, job.FromBlock, job.ToBlock); err != nil {
			slog.Error("[RECONCILE] replay failed", "from", job.FromBlock, "to", job.ToBlock, "elapsed", time.Since(start).Round(time.Millisecond), "err", err)
			if markErr := p.db.FailJob(ctx, job.ID, err, 30*time.Second); markErr != nil {
				slog.Error("[RECONCILE] mark failed error", "err", markErr)
			}
		} else {
			if markErr := p.db.CompleteJob(ctx, job.ID); markErr != nil {
				slog.Error("[RECONCILE] mark complete error", "err", markErr)
			}
			slog.Info("[RECONCILE] replay done", "from", job.FromBlock, "to", job.ToBlock, "elapsed", time.Since(start).Round(time.Millisecond))
		}
	}
}

func (p *Poller) poll(ctx context.Context) error {
	pollStart := time.Now()

	latestBlock, err := p.GetLatestBlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("getLatestBlockNumber: %w", err)
	}

	// Only process blocks that have accumulated the required number of
	// confirmations. A block at height N has (latestBlock - N) confirmations;
	// we only scan up to latestBlock - RequiredConfs so that transactions in
	// very recent blocks are never published before they are considered final.
	safeBlock := latestBlock - p.cfg.RequiredConfs

	// TRC-20 events are indexed asynchronously by the Tron node and can lag
	// behind block data by several seconds. Apply an additional offset so the
	// events API always has time to catch up before we query it.
	trc20SafeBlock := safeBlock - p.cfg.Trc20EventConfs

	slog.Debug(
		"poll tick",
		"latestBlock", latestBlock,
		"safeBlock", safeBlock,
		"trc20SafeBlock", trc20SafeBlock,
		"requiredConfs", p.cfg.RequiredConfs,
		"trc20EventConfs", p.cfg.Trc20EventConfs,
	)

	if safeBlock <= 0 {
		slog.Debug("chain not deep enough yet", "latestBlock", latestBlock, "requiredConfs", p.cfg.RequiredConfs)
		return nil
	}

	// Run TRX and every TRC-20 contract scan concurrently; each scope has an
	// independent cursor so there is no ordering dependency between them.
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := p.scanTrx(ctx, safeBlock); err != nil {
			slog.Error("scanTrx error", "err", err)
		}
	}()

	for _, contract := range p.contracts.List() {
		contract := contract
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.scanTrc20(ctx, contract, trc20SafeBlock); err != nil {
				slog.Error("scanTrc20 error", "contract", contract, "err", err)
			}
		}()
	}

	wg.Wait()

	// Drain any admin-enqueued retry jobs and replay each block range.
	// Retry scans re-publish events for the given range without touching
	// the forward-scan cursor.
	retryJobs, err := p.db.ClaimBlockRangeJobs(ctx, queueTronAdminRetry, queueWorkerID, 50)
	if err != nil {
		slog.Error("claim retry jobs", "err", err)
	} else {
		for _, job := range retryJobs {
			slog.Info("[REPLAY] starting replay", "from", job.FromBlock, "to", job.ToBlock, "blocks", job.ToBlock-job.FromBlock+1)
			start := time.Now()
			if err := p.replayScan(ctx, job.FromBlock, job.ToBlock); err != nil {
				slog.Error("[REPLAY] replay failed", "from", job.FromBlock, "to", job.ToBlock, "elapsed", time.Since(start).Round(time.Millisecond), "err", err)
				if markErr := p.db.FailJob(ctx, job.ID, err, 30*time.Second); markErr != nil {
					slog.Error("[REPLAY] mark failed error", "err", markErr)
				}
			} else {
				if markErr := p.db.CompleteJob(ctx, job.ID); markErr != nil {
					slog.Error("[REPLAY] mark complete error", "err", markErr)
				}
				slog.Info("[REPLAY] replay complete", "from", job.FromBlock, "to", job.ToBlock, "elapsed", time.Since(start).Round(time.Millisecond))
			}
		}
	}

	slog.Info("poll done", "safeBlock", safeBlock, "elapsed", time.Since(pollStart).Round(time.Millisecond))
	return nil
}

func (p *Poller) scanTrx(ctx context.Context, latestBlock int64) error {
	scope := "TRX"
	fromBlock, err := p.getHighestBlock(ctx, scope)
	if err != nil {
		return err
	}
	if fromBlock == 0 {
		if p.cfg.StartBlock > 0 {
			fromBlock = p.cfg.StartBlock
		} else {
			// No cursor and no explicit start block — begin from the current safe
			// tip so we don't replay the entire chain history on first boot.
			slog.Info("TRX no cursor found, starting from current safe tip", "safeBlock", latestBlock)
			fromBlock = latestBlock
			if err := p.saveHighestBlock(ctx, scope, fromBlock); err != nil {
				return fmt.Errorf("saveHighestBlock cold-start TRX: %w", err)
			}
		}
	}

	blocksBehind := latestBlock - fromBlock
	if blocksBehind <= 0 {
		slog.Debug("TRX up to date", "block", latestBlock)
		return nil
	}
	slog.Info("TRX scan start", "from", fromBlock+1, "to", latestBlock, "behind", blocksBehind)
	scanStart := time.Now()
	totalEvents := 0

	for batchStart := fromBlock + 1; batchStart <= latestBlock; {
		batchEnd := batchStart + blockBatchSize - 1
		if batchEnd > latestBlock {
			batchEnd = latestBlock
		}

		fetchStart := time.Now()
		blocks, err := p.getBlockRange(ctx, batchStart, batchEnd+1)
		if err != nil {
			metrics.ScanErrors.WithLabelValues("trx").Inc()
			return fmt.Errorf("getBlockRange(%d, %d): %w", batchStart, batchEnd+1, err)
		}

		for range blocks {
			metrics.BlocksScanned.Inc()
		}
		metrics.BlockScanDuration.Observe(time.Since(fetchStart).Seconds())

		// Index by block number for O(1) lookup during ordered commit.
		blockMap := make(map[int64]*tronGridBlock, len(blocks))
		for i := range blocks {
			n := blocks[i].BlockHeader.RawData.Number
			blockMap[n] = &blocks[i]
		}

		// Commit in order: find TRX transfers, publish events, advance cursor.
		commitStart := time.Now()
		batchTxTotal := 0 // total TransferContract txs seen in this range
		batchMatched := 0 // matched to a watched address
		batchEvents := 0  // successfully published to Redis
		for blockNum := batchStart; blockNum <= batchEnd; blockNum++ {
			block := blockMap[blockNum]
			var events []RawEvent
			if block != nil {
				for _, tx := range block.Transactions {
					if len(tx.RawData.Contract) == 0 {
						continue
					}
					c := tx.RawData.Contract[0]
					if c.Type != "TransferContract" {
						continue
					}
					batchTxTotal++
					toAddr := hexToBase58(c.Parameter.Value.ToAddress)
					fromAddr := hexToBase58(c.Parameter.Value.OwnerAddress)
					if !p.addresses.Contains(toAddr) && !p.addresses.Contains(fromAddr) {
						continue
					}
					amount := sunToTrx(c.Parameter.Value.Amount)
					batchMatched++
					metrics.MatchesFound.Inc()
					slog.Info(
						"[TRANSACTION] TRX",
						"address", toAddr,
						"txHash", tx.TxID,
						"from", fromAddr,
						"amount", amount,
						"block", blockNum,
					)
					events = append(events, RawEvent{
						Type:           "TRX",
						TxHash:         tx.TxID,
						FromAddress:    fromAddr,
						ToAddress:      toAddr,
						Amount:         amount,
						BlockNumber:    blockNum,
						BlockTimestamp: block.BlockHeader.RawData.Timestamp,
					})
				}
			} else {
				slog.Debug("TRX block missing from range response", "block", blockNum)
			}
			if len(events) > 0 {
				if n, err := p.enqueueOutboxEvents(ctx, scope, events); err != nil {
					slog.Error("enqueue TRX events", "block", blockNum, "count", len(events), "err", err)
				} else {
					metrics.EventsPublished.Add(float64(n))
					batchEvents += n
				}
			}
			if err := p.saveHighestBlock(ctx, scope, blockNum); err != nil {
				return fmt.Errorf("saveHighestBlock(%d): %w", blockNum, err)
			}
		}
		totalEvents += batchEvents
		slog.Debug(
			"TRX batch",
			"from", batchStart, "to", batchEnd,
			"txTotal", batchTxTotal,
			"matched", batchMatched,
			"published", batchEvents,
			"elapsed", time.Since(commitStart).Round(time.Millisecond),
		)
		batchStart = batchEnd + 1
	}
	slog.Info(
		"TRX scan done",
		"scanned", blocksBehind,
		"events", totalEvents,
		"elapsed", time.Since(scanStart).Round(time.Millisecond),
	)
	return nil
}

func (p *Poller) scanTrc20(ctx context.Context, contract string, latestBlock int64) error {
	scope := contract
	fromBlock, err := p.getHighestBlock(ctx, scope)
	if err != nil {
		return err
	}
	if fromBlock == 0 {
		if p.cfg.StartBlock > 0 {
			fromBlock = p.cfg.StartBlock
		} else {
			// No cursor and no explicit start block — begin from the current safe
			// tip so we don't replay the entire chain history on first boot.
			slog.Info("TRC20 no cursor found, starting from current safe tip", "contract", contract, "safeBlock", latestBlock)
			fromBlock = latestBlock
			if err := p.saveHighestBlock(ctx, scope, fromBlock); err != nil {
				return fmt.Errorf("saveHighestBlock cold-start TRC20 %s: %w", contract, err)
			}
		}
	}

	blocksBehind := latestBlock - fromBlock
	if blocksBehind <= 0 {
		slog.Debug("TRC20 up to date", "contract", contract, "block", latestBlock)
		return nil
	}
	slog.Info("TRC20 scan start", "contract", contract, "from", fromBlock+1, "to", latestBlock, "behind", blocksBehind)
	scanStart := time.Now()
	totalEvents := 0

	for batchStart := fromBlock + 1; batchStart <= latestBlock; {
		batchEnd := batchStart + blockBatchSize - 1
		if batchEnd > latestBlock {
			batchEnd = latestBlock
		}

		slog.Debug("TRC20 block range fetch", "contract", contract, "from", batchStart, "to", batchEnd)
		fetchStart := time.Now()

		// Fetch block range to obtain per-block timestamps for the events API query.
		blocks, err := p.getBlockRange(ctx, batchStart, batchEnd+1)
		if err != nil {
			return fmt.Errorf("getBlockRange(%d, %d): %w", batchStart, batchEnd+1, err)
		}
		slog.Debug(
			"TRC20 block range fetched",
			"contract", contract,
			"from", batchStart, "to", batchEnd,
			"returned", len(blocks),
			"elapsed", time.Since(fetchStart).Round(time.Millisecond),
		)

		if len(blocks) == 0 {
			slog.Warn("TRC20 getBlockRange returned 0 blocks, will retry range on next poll",
				"contract", contract, "from", batchStart, "to", batchEnd)
			// Do not advance the cursor — empty block responses are often transient.
			break
		}

		// Extract the timestamp range covered by this block batch.
		var minTs, maxTs int64
		for _, b := range blocks {
			ts := b.BlockHeader.RawData.Timestamp
			if minTs == 0 || ts < minTs {
				minTs = ts
			}
			if ts > maxTs {
				maxTs = ts
			}
		}
		// Fetch all Transfer events in this timestamp range with fingerprint pagination.
		allEvents, err := p.fetchTrc20EventsWithRetry(ctx, contract, minTs, maxTs, len(blocks))
		if err != nil {
			return fmt.Errorf("fetchTrc20EventsWithRetry(%s): %w", contract, err)
		}

		// Group matching events by block number for ordered commit.
		// Track eventsTotal (all from chain in this window) and matchedCount.
		slog.Debug(
			"TRC20 events fetched",
			"contract", contract,
			"from", batchStart, "to", batchEnd,
			"count", len(allEvents),
		)
		eventsByBlock := make(map[int64][]RawEvent)
		seenTxHashes := make(map[string]struct{}) // deduplicate: one event per txHash
		eventsTotal := 0                          // events from chain within this block window
		matchedCount := 0                         // matched to a watched address
		for _, e := range allEvents {
			// Guard: only include events within our batch window.
			if e.BlockNumber < batchStart || e.BlockNumber > batchEnd {
				continue
			}
			eventsTotal++
			// TronGrid's events API may return addresses as hex ("41…") rather than
			// base58. Normalise before the HashSet lookup so both formats match.
			toAddr := hexToBase58(e.Result.To)
			fromAddr := hexToBase58(e.Result.From)
			if !p.addresses.Contains(toAddr) && !p.addresses.Contains(fromAddr) {
				continue
			}
			// A single transaction can emit multiple Transfer events (e.g. multi-hop).
			// Publish only the first matching event per txHash — the processor handles
			// both sender and receiver from the one event.
			if _, seen := seenTxHashes[e.TransactionID]; seen {
				slog.Debug("TRC20 duplicate txHash skipped", "txHash", e.TransactionID, "contract", contract)
				continue
			}
			seenTxHashes[e.TransactionID] = struct{}{}
			matchedCount++
			metrics.MatchesFound.Inc()
			slog.Info(
				"[EVENT] TRC20",
				"address", toAddr,
				"txHash", e.TransactionID,
				"from", fromAddr,
				"amount", e.Result.Value,
				"contract", contract,
				"block", e.BlockNumber,
			)
			eventsByBlock[e.BlockNumber] = append(eventsByBlock[e.BlockNumber], RawEvent{
				Type:                 "TRC20",
				TxHash:               e.TransactionID,
				FromAddress:          fromAddr,
				ToAddress:            toAddr,
				Amount:               e.Result.Value,
				TokenContractAddress: contract,
				BlockNumber:          e.BlockNumber,
				BlockTimestamp:       e.BlockTimestamp,
			})
		}

		// Commit: publish events per block, then advance cursor with a retain window
		// so the next poll re-queries recent blocks while TronGrid finishes indexing.
		commitStart := time.Now()
		batchEvents := 0
		for blockNum := batchStart; blockNum <= batchEnd; blockNum++ {
			if evts := eventsByBlock[blockNum]; len(evts) > 0 {
				if n, err := p.enqueueOutboxEvents(ctx, scope, evts); err != nil {
					slog.Error("enqueue TRC20 events", "contract", contract, "block", blockNum, "count", len(evts), "err", err)
				} else {
					metrics.EventsPublished.Add(float64(n))
					batchEvents += n
				}
			}
		}
		cursorBlock := p.trc20CursorAfterBatch(fromBlock, batchStart, batchEnd)
		if err := p.saveHighestBlock(ctx, scope, cursorBlock); err != nil {
			return fmt.Errorf("saveHighestBlock(%d): %w", cursorBlock, err)
		}
		fromBlock = cursorBlock
		totalEvents += batchEvents
		slog.Debug(
			"TRC20 batch",
			"contract", contract,
			"from", batchStart, "to", batchEnd,
			"cursor", cursorBlock,
			"eventsTotal", eventsTotal,
			"matched", matchedCount,
			"published", batchEvents,
			"elapsed", time.Since(commitStart).Round(time.Millisecond),
		)
		batchStart = batchEnd + 1
	}
	slog.Info(
		"TRC20 scan done",
		"contract", contract,
		"scanned", blocksBehind,
		"events", totalEvents,
		"elapsed", time.Since(scanStart).Round(time.Millisecond),
	)
	return nil
}

// replayScan re-scans a fixed block range for TRX and all watched TRC-20
// contracts, re-publishing any matched events to Redis. Unlike the normal
// forward scan, replayScan does NOT read or update the per-scope cursor, so
// it cannot corrupt the forward-scan position.
func (p *Poller) replayScan(ctx context.Context, fromBlock, toBlock int64) error {
	if fromBlock > toBlock {
		return fmt.Errorf("replayScan: fromBlock(%d) > toBlock(%d)", fromBlock, toBlock)
	}

	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex

	setErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	// TRX replay
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := p.replayTrxRange(ctx, fromBlock, toBlock); err != nil {
			slog.Error("replayTrxRange", "from", fromBlock, "to", toBlock, "err", err)
			setErr(err)
		}
	}()

	// TRC-20 replay for each contract
	for _, contract := range p.contracts.List() {
		contract := contract
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.replayTrc20Range(ctx, contract, fromBlock, toBlock); err != nil {
				slog.Error("replayTrc20Range", "contract", contract, "from", fromBlock, "to", toBlock, "err", err)
				setErr(err)
			}
		}()
	}

	wg.Wait()
	return firstErr
}

// replayTrxRange fetches TRX transfers in [fromBlock, toBlock] and publishes
// matching events. Cursor is never read or written.
func (p *Poller) replayTrxRange(ctx context.Context, fromBlock, toBlock int64) error {
	for batchStart := fromBlock; batchStart <= toBlock; {
		batchEnd := batchStart + blockBatchSize - 1
		if batchEnd > toBlock {
			batchEnd = toBlock
		}
		slog.Debug("[REPLAY] TRX batch", "batchStart", batchStart, "batchEnd", batchEnd, "rangeEnd", toBlock)
		blocks, err := p.getBlockRange(ctx, batchStart, batchEnd+1)
		if err != nil {
			return fmt.Errorf("getBlockRange(%d, %d): %w", batchStart, batchEnd+1, err)
		}
		blockMap := make(map[int64]*tronGridBlock, len(blocks))
		for i := range blocks {
			n := blocks[i].BlockHeader.RawData.Number
			blockMap[n] = &blocks[i]
		}
		for blockNum := batchStart; blockNum <= batchEnd; blockNum++ {
			block := blockMap[blockNum]
			if block == nil {
				continue
			}
			var events []RawEvent
			for _, tx := range block.Transactions {
				if len(tx.RawData.Contract) == 0 {
					continue
				}
				c := tx.RawData.Contract[0]
				if c.Type != "TransferContract" {
					continue
				}
				toAddr := hexToBase58(c.Parameter.Value.ToAddress)
				fromAddr := hexToBase58(c.Parameter.Value.OwnerAddress)
				if !p.addresses.Contains(toAddr) && !p.addresses.Contains(fromAddr) {
					continue
				}
				amount := sunToTrx(c.Parameter.Value.Amount)
				slog.Info(
					"[REPLAY][TRANSACTION] TRX",
					"address", toAddr,
					"txHash", tx.TxID,
					"from", fromAddr,
					"amount", amount,
					"block", blockNum,
				)
				events = append(events, RawEvent{
					Type:           "TRX",
					TxHash:         tx.TxID,
					FromAddress:    fromAddr,
					ToAddress:      toAddr,
					Amount:         amount,
					BlockNumber:    blockNum,
					BlockTimestamp: block.BlockHeader.RawData.Timestamp,
				})
			}
			if len(events) > 0 {
				slog.Debug("[REPLAY] TRX events matched", "block", blockNum, "count", len(events))
				if _, err := p.enqueueOutboxEvents(ctx, "TRX", events); err != nil {
					slog.Error("[REPLAY] TRX enqueue failed", "block", blockNum, "err", err)
				}
			}
		}
		batchStart = batchEnd + 1
	}
	return nil
}

// replayTrc20Range fetches TRC-20 Transfer events for [fromBlock, toBlock]
// and publishes matching events. Cursor is never read or written.
func (p *Poller) replayTrc20Range(ctx context.Context, contract string, fromBlock, toBlock int64) error {
	for batchStart := fromBlock; batchStart <= toBlock; {
		batchEnd := batchStart + blockBatchSize - 1
		if batchEnd > toBlock {
			batchEnd = toBlock
		}
		slog.Debug("[REPLAY] TRC20 batch", "contract", contract, "batchStart", batchStart, "batchEnd", batchEnd, "rangeEnd", toBlock)
		blocks, err := p.getBlockRange(ctx, batchStart, batchEnd+1)
		if err != nil {
			return fmt.Errorf("getBlockRange(%d, %d): %w", batchStart, batchEnd+1, err)
		}
		if len(blocks) == 0 {
			slog.Debug("[REPLAY] TRC20 batch empty (no blocks returned)", "contract", contract, "batchStart", batchStart, "batchEnd", batchEnd)
			batchStart = batchEnd + 1
			continue
		}
		var minTs, maxTs int64
		for _, b := range blocks {
			ts := b.BlockHeader.RawData.Timestamp
			if minTs == 0 || ts < minTs {
				minTs = ts
			}
			if ts > maxTs {
				maxTs = ts
			}
		}
		allEvents, err := p.getAllTrc20EventsByTimeRange(ctx, contract, minTs, maxTs)
		if err != nil {
			return fmt.Errorf("getAllTrc20EventsByTimeRange(%s): %w", contract, err)
		}
		slog.Debug(
			"[REPLAY] TRC20 events fetched",
			"contract", contract,
			"from", batchStart, "to", batchEnd,
			"count", len(allEvents),
		)
		eventsByBlock := make(map[int64][]RawEvent)
		seenTxHashes := make(map[string]struct{})
		matchedCount := 0
		for _, e := range allEvents {
			if e.BlockNumber < batchStart || e.BlockNumber > batchEnd {
				continue
			}
			toAddr := hexToBase58(e.Result.To)
			fromAddr := hexToBase58(e.Result.From)
			toMatched := p.addresses.Contains(toAddr)
			fromMatched := p.addresses.Contains(fromAddr)
			slog.Debug(
				"[REPLAY] TRC20 event candidate",
				"rawTo", e.Result.To,
				"rawFrom", e.Result.From,
				"toAddr", toAddr,
				"fromAddr", fromAddr,
				"txHash", e.TransactionID,
				"contract", contract,
				"block", e.BlockNumber,
				"toInWatchlist", toMatched,
				"fromInWatchlist", fromMatched,
			)
			if !toMatched && !fromMatched {
				continue
			}
			if _, seen := seenTxHashes[e.TransactionID]; seen {
				slog.Debug("[REPLAY] TRC20 duplicate txHash skipped", "txHash", e.TransactionID, "contract", contract)
				continue
			}
			seenTxHashes[e.TransactionID] = struct{}{}
			matchedCount++
			slog.Info(
				"[REPLAY][EVENT] TRC20",
				"address", toAddr,
				"txHash", e.TransactionID,
				"from", fromAddr,
				"amount", e.Result.Value,
				"contract", contract,
				"block", e.BlockNumber,
			)
			eventsByBlock[e.BlockNumber] = append(eventsByBlock[e.BlockNumber], RawEvent{
				Type:                 "TRC20",
				TxHash:               e.TransactionID,
				FromAddress:          fromAddr,
				ToAddress:            toAddr,
				Amount:               e.Result.Value,
				TokenContractAddress: contract,
				BlockNumber:          e.BlockNumber,
				BlockTimestamp:       e.BlockTimestamp,
			})
		}
		publishedCount := 0
		for blockNum := batchStart; blockNum <= batchEnd; blockNum++ {
			if evts := eventsByBlock[blockNum]; len(evts) > 0 {
				slog.Debug("[REPLAY] TRC20 events matched", "contract", contract, "block", blockNum, "count", len(evts))
				if _, err := p.enqueueOutboxEvents(ctx, contract, evts); err != nil {
					slog.Error("[REPLAY] TRC20 enqueue failed", "contract", contract, "block", blockNum, "err", err)
				} else {
					publishedCount += len(evts)
				}
			}
		}
		slog.Debug(
			"[REPLAY] TRC20 batch done",
			"contract", contract,
			"from", batchStart, "to", batchEnd,
			"fetched", len(allEvents),
			"matched", matchedCount,
			"published", publishedCount,
		)
		batchStart = batchEnd + 1
	}
	return nil
}

// getHighestBlock returns the highest scanned block for a scope from Postgres.
func (p *Poller) getHighestBlock(ctx context.Context, scope string) (int64, error) {
	return p.db.GetScannedBlock(ctx, scope)
}

// saveHighestBlock persists the highest scanned block to Postgres.
func (p *Poller) saveHighestBlock(ctx context.Context, scope string, blockNum int64) error {
	return p.db.SetScannedBlock(ctx, scope, blockNum)
}

func (p *Poller) enqueueOutboxEvents(ctx context.Context, scope string, events []RawEvent) (int, error) {
	enqueued := 0
	for _, ev := range events {
		id, err := p.outbox.EnqueueWebhookEvent(ctx, ev.Type, scope, ev.TxHash, ev.BlockNumber, ev.BlockTimestamp, rawToWebhookPayload("", ev))
		if err != nil {
			return enqueued, err
		}
		if id != "" {
			enqueued++
		}
	}
	return enqueued, nil
}

// GetLatestBlockNumber calls TronGrid to get the current block number.
func (p *Poller) GetLatestBlockNumber(ctx context.Context) (int64, error) {
	url := p.cfg.TronGridBaseURL + "/wallet/getnowblock"
	var block tronGridBlock
	if err := p.tronGridDo(ctx, http.MethodGet, url, nil, &block); err != nil {
		return 0, err
	}
	return block.BlockHeader.RawData.Number, nil
}

// getBlockRange fetches a range of blocks in a single request using
// /wallet/getblockbylimitnext. startNum is inclusive, endNum is exclusive.
// TronGrid allows a maximum of 100 blocks per request (endNum−startNum ≤ 100).
func (p *Poller) getBlockRange(ctx context.Context, startNum, endNum int64) ([]tronGridBlock, error) {
	url := p.cfg.TronGridBaseURL + "/wallet/getblockbylimitnext"
	reqBody := map[string]int64{"startNum": startNum, "endNum": endNum}
	var resp tronGridBlockRangeResp
	if err := p.tronGridDo(ctx, http.MethodPost, url, reqBody, &resp); err != nil {
		return nil, err
	}
	return resp.Block, nil
}

// trc20CursorAfterBatch returns the highest block to persist after processing
// [batchStart, batchEnd]. The cursor stays retain blocks behind batchEnd so the
// forward scan re-queries that window on the next poll (TronGrid event-index lag).
func (p *Poller) trc20CursorAfterBatch(fromBlock, batchStart, batchEnd int64) int64 {
	retain := p.cfg.Trc20CursorRetain
	if retain <= 0 {
		return batchEnd
	}
	candidate := batchEnd - retain
	if candidate < batchStart-1 {
		candidate = batchStart - 1
	}
	if candidate < fromBlock {
		return fromBlock
	}
	return candidate
}

// fetchTrc20EventsWithRetry calls the TronGrid events API, retrying when blocks
// were loaded but the API returned no events (typical indexing lag).
func (p *Poller) fetchTrc20EventsWithRetry(ctx context.Context, contract string, minTs, maxTs int64, blockCount int) ([]tronGridTrc20Event, error) {
	retries := p.cfg.Trc20EventRetries
	if retries <= 0 {
		retries = 3
	}
	delay := time.Duration(p.cfg.Trc20EventRetryDelayMs) * time.Millisecond
	if delay <= 0 {
		delay = 2 * time.Second
	}

	var (
		all []tronGridTrc20Event
		err error
	)
	for attempt := 1; attempt <= retries; attempt++ {
		all, err = p.getAllTrc20EventsByTimeRange(ctx, contract, minTs, maxTs)
		if err != nil {
			return nil, err
		}
		if len(all) > 0 || blockCount == 0 || attempt == retries {
			break
		}
		slog.Debug(
			"TRC20 events API empty for block batch, retrying",
			"contract", contract,
			"attempt", attempt,
			"retries", retries,
			"delay", delay,
		)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return all, nil
}

// getAllTrc20EventsByTimeRange fetches all Transfer events for a TRC-20 contract
// within a millisecond timestamp range, following fingerprint-based pagination
// until TronGrid reports no more results.
func (p *Poller) getAllTrc20EventsByTimeRange(ctx context.Context, contract string, minTs, maxTs int64) ([]tronGridTrc20Event, error) {
	var all []tronGridTrc20Event
	fingerprint := ""
	for page := 1; ; page++ {
		url := fmt.Sprintf(
			"%s/v1/contracts/%s/events?event_name=Transfer&min_timestamp=%d&max_timestamp=%d&limit=200&only_confirmed=true&order_by=block_timestamp,asc",
			p.cfg.TronGridBaseURL, contract, minTs, maxTs,
		)
		if fingerprint != "" {
			url += "&fingerprint=" + fingerprint
		}
		var resp tronGridTrc20EventsResp
		if err := p.tronGridDo(ctx, http.MethodGet, url, nil, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Data...)
		slog.Debug(
			"TRC20 events page",
			"contract", contract,
			"page", page,
			"count", len(resp.Data),
			"hasMore", resp.Meta.Fingerprint != "",
		)
		if resp.Meta.Fingerprint == "" || len(resp.Data) == 0 {
			break
		}
		fingerprint = resp.Meta.Fingerprint
	}
	return all, nil
}

// tronGridDo executes a TronGrid HTTP request (GET or POST) with semaphore-based
// rate limiting and exponential backoff on 429 responses.
func (p *Poller) tronGridDo(ctx context.Context, method, url string, reqBody any, out any) error {
	// Acquire the global concurrency semaphore before touching the network.
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-p.sem }()

	const maxRetries = 4
	backoff := 500 * time.Millisecond
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			slog.Warn(
				"TronGrid rate limited, backing off",
				"method", method,
				"url", url,
				"attempt", attempt,
				"backoff", backoff,
			)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			backoff *= 2
		}

		var bodyReader io.Reader
		if reqBody != nil {
			data, err := json.Marshal(reqBody)
			if err != nil {
				return fmt.Errorf("marshal request body: %w", err)
			}
			bodyReader = bytes.NewReader(data)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if reqBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if p.cfg.TronGridAPIKey != "" {
			req.Header.Set("TRON-PRO-API-KEY", p.cfg.TronGridAPIKey)
		}
		resp, err := p.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, url, err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			_ = resp.Body.Close()
			continue
		}
		rawBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s %s status %d: %s", method, url, resp.StatusCode, rawBody)
		}
		if readErr != nil {
			return fmt.Errorf("read response body: %w", readErr)
		}
		return json.Unmarshal(rawBody, out)
	}
	return fmt.Errorf("%s %s: rate limited after %d retries", method, url, maxRetries)
}

// hexToBase58 converts a Tron address in any encoding to its base58check form.
// Handles three input formats:
//   - Already base58 ("T…")             → returned as-is
//   - Tron hex with version byte ("41…") → decoded directly
//   - Ethereum ABI hex ("0x…", 20 bytes) → "41" prepended then decoded
func hexToBase58(hexAddr string) string {
	if len(hexAddr) == 0 {
		return hexAddr
	}
	if hexAddr[0] == 'T' {
		return hexAddr
	}
	// TronGrid's events API returns addresses in Ethereum ABI format: "0x" + 20 bytes.
	// Tron's native hex format is "41" + 20 bytes (21 bytes total).
	if len(hexAddr) > 2 && hexAddr[:2] == "0x" {
		hexAddr = "41" + hexAddr[2:]
	}
	return tronHexToBase58(hexAddr)
}

// sunToTrx converts an integer sun amount to a decimal TRX string.
func sunToTrx(sun int64) string {
	trx := float64(sun) / 1_000_000
	return fmt.Sprintf("%.6f", trx)
}
