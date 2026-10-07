package scanner

// trxTransferLeg is a parsed native TRX TransferContract in a block.
type trxTransferLeg struct {
	txID     string
	fromAddr string
	toAddr   string
	amount   string
	blockTS  int64
}

func parseTrxTransferLegs(block *tronGridBlock) []trxTransferLeg {
	if block == nil {
		return nil
	}
	ts := block.BlockHeader.RawData.Timestamp
	legs := make([]trxTransferLeg, 0, len(block.Transactions))
	for _, tx := range block.Transactions {
		if len(tx.RawData.Contract) == 0 {
			continue
		}
		c := tx.RawData.Contract[0]
		if c.Type != "TransferContract" {
			continue
		}
		legs = append(legs, trxTransferLeg{
			txID:     tx.TxID,
			fromAddr: hexToBase58(c.Parameter.Value.OwnerAddress),
			toAddr:   hexToBase58(c.Parameter.Value.ToAddress),
			amount:   sunToTrx(c.Parameter.Value.Amount),
			blockTS:  ts,
		})
	}
	return legs
}

func bloomPrefetchFromTrxLegs(addresses AddressSet, legs []trxTransferLeg) []string {
	var prefetch []string
	for _, leg := range legs {
		prefetch = append(prefetch, bloomConfirmCandidates(addresses, leg.fromAddr, leg.toAddr)...)
	}
	return prefetch
}
