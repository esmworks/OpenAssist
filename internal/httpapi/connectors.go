package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// connectorDTO — mühürlü token ASLA dışarı verilmez (§5).
type connectorDTO struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	Account   string    `json:"account"`
	Status    string    `json:"status"`
	Scopes    []string  `json:"scopes"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toConnectorDTO(c db.Connector) connectorDTO {
	return connectorDTO{
		ID: c.ID, Kind: c.Kind, Account: c.Account, Status: c.Status,
		Scopes: c.Scopes, Error: c.Error,
		CreatedAt: c.CreatedAt.UTC(), UpdatedAt: c.UpdatedAt.UTC(),
	}
}

func (a *api) listConnectors(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.Q.ListConnectors(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]connectorDTO, 0, len(rows))
	for _, c := range rows {
		out = append(out, toConnectorDTO(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) deleteConnector(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz konektör id")
		return
	}
	n, err := a.st.Q.DeleteConnector(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		writeErr(w, http.StatusNotFound, "konektör bulunamadı")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
