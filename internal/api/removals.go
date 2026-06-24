package api

import (
	"errors"
	"net/http"
	"strings"

	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/validate"
)

func (s *Server) handleDeleteAddress(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimSpace(r.PathValue("address"))
	if !validate.TronAddress(address) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Tron address"})
		return
	}

	row, err := s.db.DeactivateWatchedAddress(r.Context(), address)
	if err != nil {
		if errors.Is(err, internaldb.ErrWatchedAddressNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "address not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to deactivate address"})
		return
	}
	if err := s.addresses.Reload(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh address store"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":        row.ID,
		"address":   row.Address,
		"status":    row.Status,
		"updatedAt": row.UpdatedAt.UTC().Format(timeRFC3339),
	})
}

func (s *Server) handleDeleteContract(w http.ResponseWriter, r *http.Request) {
	contract := strings.TrimSpace(r.PathValue("contractAddress"))
	if !validate.TronAddress(contract) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Tron contract address"})
		return
	}

	row, err := s.db.DeactivateWatchedContract(r.Context(), contract)
	if err != nil {
		if errors.Is(err, internaldb.ErrWatchedContractNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "contract not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to deactivate contract"})
		return
	}
	if err := s.contracts.Reload(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to refresh contract store"})
		return
	}
	resp := map[string]any{
		"id":              row.ID,
		"contractAddress": row.ContractAddress,
		"status":          row.Status,
		"updatedAt":       row.UpdatedAt.UTC().Format(timeRFC3339),
	}
	if row.TokenSymbol != nil {
		resp["tokenSymbol"] = *row.TokenSymbol
	}
	writeJSON(w, http.StatusOK, resp)
}
