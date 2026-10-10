package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	hive "github.com/sfborg/hive/pkg"
	"github.com/sfborg/hive/pkg/refmanager/sidecar"
	"github.com/sfborg/hive/pkg/taxonomy/mentions"
)

// apiNameSource is a name source the archive's name-mention index uses.
type apiNameSource struct {
	ID    int    `json:"id"`
	Key   string `json:"key"`
	Label string `json:"label"`
	Short string `json:"short,omitempty"`
}

// apiAvailableSource is a data source GNverifier offers.
type apiAvailableSource struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Short       string `json:"short,omitempty"`
	Curation    string `json:"curation,omitempty"`
	Records     int    `json:"records,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

// apiReindexStatus reports the name-mention re-index job.
type apiReindexStatus struct {
	Running    bool   `json:"running"`
	Stage      string `json:"stage,omitempty"`
	Done       int    `json:"done"`
	Total      int    `json:"total"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
	Papers     int    `json:"papers,omitempty"`
	Names      int    `json:"names,omitempty"`
}

// reindexJob runs one name-mention re-index at a time in the
// background and keeps its progress for the status endpoint.
type reindexJob struct {
	mu     sync.Mutex
	status apiReindexStatus
}

func (j *reindexJob) snapshot() apiReindexStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

// start launches a re-index unless one is running; it reports whether
// it started.
func (j *reindexJob) start(a *hive.Archive) bool {
	j.mu.Lock()
	if j.status.Running {
		j.mu.Unlock()
		return false
	}
	j.status = apiReindexStatus{Running: true, Stage: "Starting", StartedAt: time.Now().UTC().Format(time.RFC3339)}
	j.mu.Unlock()
	go func() {
		ctx := hive.WithActor(context.Background(), "hive")
		read := func(sha string) ([]byte, error) { return sidecar.Read(a.Path(), sha, "jats.xml") }
		verifier := func(id int) mentions.Verifier { return mentions.NewVerifier(id) }
		rep, err := a.IndexNameMentions(ctx, nil, read, verifier, func(p hive.IndexProgress) {
			j.mu.Lock()
			j.status.Stage, j.status.Done, j.status.Total = p.Stage, p.Done, p.Total
			j.mu.Unlock()
		})
		j.mu.Lock()
		defer j.mu.Unlock()
		j.status.Running = false
		j.status.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		if err != nil {
			j.status.Error = err.Error()
			slog.Warn("name-mention re-index failed", "err", err)
			return
		}
		j.status.Stage = "Done"
		j.status.Papers, j.status.Names = rep.Papers, rep.Names
	}()
	return true
}

// availableSources caches GNverifier's data source list for an hour.
type availableSources struct {
	mu      sync.Mutex
	items   []apiAvailableSource
	fetched time.Time
}

func (c *availableSources) get() ([]apiAvailableSource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items != nil && time.Since(c.fetched) < time.Hour {
		return c.items, nil
	}
	ds, err := mentions.DataSources()
	if err != nil {
		return nil, fmt.Errorf("GNverifier data sources: %w", err)
	}
	items := make([]apiAvailableSource, len(ds))
	for i, d := range ds {
		items[i] = apiAvailableSource{ID: d.ID, Title: d.Title, Short: d.TitleShort,
			Curation: d.Curation.String(), Records: d.RecordCount,
			Description: d.Description, URL: d.WebsiteURL}
	}
	c.items, c.fetched = items, time.Now()
	return items, nil
}

func nameSourcesToAPI(sources []mentions.Source) []apiNameSource {
	out := make([]apiNameSource, len(sources))
	for i, s := range sources {
		out[i] = apiNameSource{ID: s.ID, Key: s.Key, Label: s.Label, Short: s.Short}
	}
	return out
}

// handleGetNameSources returns the archive's name sources and the
// re-index job's status.
//
// @Summary      Name sources
// @Tags         name-mentions
// @Produce      json
// @Success      200 {object} map[string]any
// @Router       /name-sources [get]
func (s *server) handleGetNameSources(w http.ResponseWriter, r *http.Request) {
	sources, err := s.a.NameSources(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sources": nameSourcesToAPI(sources),
		"reindex": s.reindex.snapshot(),
	})
}

// handleAvailableNameSources lists the data sources GNverifier offers.
//
// @Summary      GNverifier data sources
// @Tags         name-mentions
// @Produce      json
// @Success      200 {object} map[string]any
// @Failure      502 {object} problem
// @Router       /name-sources/available [get]
func (s *server) handleAvailableNameSources(w http.ResponseWriter, r *http.Request) {
	items, err := s.available.get()
	if err != nil {
		writeProblemDetail(w, r, http.StatusBadGateway, "urn:hive:error:gnverifier", "GNverifier unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handlePutNameSources stores the archive's name sources, in
// preference order, and starts re-indexing the references against them.
//
// @Summary      Set name sources
// @Tags         name-mentions
// @Accept       json
// @Produce      json
// @Param        body body object true "{ids: [GNverifier data source IDs]}"
// @Success      200 {object} map[string]any
// @Failure      400 {object} problem
// @Router       /name-sources [put]
func (s *server) handlePutNameSources(w http.ResponseWriter, r *http.Request) {
	if s.a.IsReadOnly() {
		writeProblem(w, r, hive.ErrReadOnly)
		return
	}
	var body struct {
		IDs []int `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	labels := map[int]apiAvailableSource{}
	if items, err := s.available.get(); err == nil {
		for _, it := range items {
			labels[it.ID] = it
		}
	}
	sources := make([]mentions.Source, len(body.IDs))
	for i, id := range body.IDs {
		src := mentions.Source{ID: id, Key: mentions.KeyFor(id)}
		if known, ok := mentions.SourceByID(mentions.DefaultSources, id); ok {
			src = known
		} else if it, ok := labels[id]; ok {
			src.Label, src.Short = it.Title, it.Short
		}
		sources[i] = src
	}
	if err := s.a.WithTx(r.Context(), func(tx *hive.Tx) error { return tx.SetNameSources(sources) }); err != nil {
		writeProblem(w, r, err)
		return
	}
	s.reindex.start(s.a)
	stored, err := s.a.NameSources(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sources": nameSourcesToAPI(stored),
		"reindex": s.reindex.snapshot(),
	})
}

// handleReindexNameMentions starts re-indexing every reference against
// the archive's name sources; 409 when a re-index is already running.
//
// @Summary      Re-index name mentions
// @Tags         name-mentions
// @Produce      json
// @Success      202 {object} apiReindexStatus
// @Failure      409 {object} problem
// @Router       /name-sources/reindex [post]
func (s *server) handleReindexNameMentions(w http.ResponseWriter, r *http.Request) {
	if s.a.IsReadOnly() {
		writeProblem(w, r, hive.ErrReadOnly)
		return
	}
	if !s.reindex.start(s.a) {
		writeProblemDetail(w, r, http.StatusConflict, "urn:hive:error:reindex-running",
			"re-index running", "a re-index is already running")
		return
	}
	writeJSON(w, http.StatusAccepted, s.reindex.snapshot())
}

// handleReindexStatus reports the re-index job.
//
// @Summary      Re-index status
// @Tags         name-mentions
// @Produce      json
// @Success      200 {object} apiReindexStatus
// @Router       /name-sources/reindex [get]
func (s *server) handleReindexStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.reindex.snapshot())
}
