package httpapi

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/sales"
	"github.com/hussein/ai-salesperson/internal/sources"
	"github.com/hussein/ai-salesperson/internal/store"
)

func (a *API) dashboard(w http.ResponseWriter, r *http.Request) {
	s, err := a.sales.Stats(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *API) analyzeWebsite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Website string `json:"website"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := a.sales.AnalyzeWebsite(r.Context(), orgID(r), in.Website)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) listSources(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, sources.Names())
}

// ---- Agents -----------------------------------------------------------------

type agentBody struct {
	Name             string                 `json:"name"`
	Persona          string                 `json:"persona"`
	Channel          string                 `json:"channel"`
	Language         string                 `json:"language"`
	BookingURL       string                 `json:"booking_url"`
	Profile          domain.BusinessProfile `json:"profile"`
	Targeting        domain.Targeting       `json:"targeting"`
	RequireApproval  *bool                  `json:"require_approval"`
	Autopilot        *bool                  `json:"autopilot"`
	MinScore         *int                   `json:"min_score"`
	DailySendLimit   *int                   `json:"daily_send_limit"`
	AutoDiscover     *bool                  `json:"auto_discover"`
	DiscoverySource  string                 `json:"discovery_source"`
	DefaultDealValue *float64               `json:"default_deal_value"`
}

func (b agentBody) input() sales.AgentInput {
	return sales.AgentInput{
		Name: b.Name, Persona: b.Persona, Channel: b.Channel, Language: b.Language, BookingURL: b.BookingURL,
		Profile: b.Profile, Targeting: b.Targeting, RequireApproval: b.RequireApproval, Autopilot: b.Autopilot,
		MinScore: b.MinScore, DailySendLimit: b.DailySendLimit, AutoDiscover: b.AutoDiscover,
		DiscoverySource: b.DiscoverySource, DefaultDealValue: b.DefaultDealValue,
	}
}

func (a *API) listAgents(w http.ResponseWriter, r *http.Request) {
	as, err := a.store.ListAgents(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, as)
}

func (a *API) createAgent(w http.ResponseWriter, r *http.Request) {
	var in agentBody
	if !decode(w, r, &in) {
		return
	}
	ag, err := a.sales.CreateAgent(r.Context(), orgID(r), in.input())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ag)
}

func (a *API) getAgent(w http.ResponseWriter, r *http.Request) {
	ag, err := a.store.GetAgent(r.Context(), orgID(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ag)
}

func (a *API) updateAgent(w http.ResponseWriter, r *http.Request) {
	var in agentBody
	if !decode(w, r, &in) {
		return
	}
	ag, err := a.sales.UpdateAgent(r.Context(), orgID(r), r.PathValue("id"), in.input())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ag)
}

func (a *API) generateBrain(w http.ResponseWriter, r *http.Request) {
	ag, err := a.sales.GenerateBrain(r.Context(), orgID(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ag)
}

func (a *API) setStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status domain.AgentStatus `json:"status"`
	}
	if !decode(w, r, &in) {
		return
	}
	ag, err := a.sales.SetAgentStatus(r.Context(), orgID(r), r.PathValue("id"), in.Status)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ag)
}

// ---- Prospects --------------------------------------------------------------

// importProspects accepts JSON {"prospects":[...]} or a text/csv upload.
func (a *API) importProspects(w http.ResponseWriter, r *http.Request) {
	var list []domain.Prospect
	if strings.HasPrefix(r.Header.Get("Content-Type"), "text/csv") {
		var err error
		if list, err = sources.ParseCSV(http.MaxBytesReader(w, r.Body, 5<<20)); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		var in struct {
			Prospects []domain.Prospect `json:"prospects"`
		}
		if !decode(w, r, &in) {
			return
		}
		list = in.Prospects
	}
	if len(list) == 0 {
		writeErr(w, http.StatusBadRequest, "no prospects supplied")
		return
	}
	if len(list) > 5000 {
		writeErr(w, http.StatusBadRequest, "at most 5000 prospects per import")
		return
	}
	res, err := a.sales.ImportProspects(r.Context(), orgID(r), r.PathValue("id"), "customer_list", list)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// listProspects returns an agent's prospects, best lead score first.
func (a *API) listProspects(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.store.GetAgent(r.Context(), orgID(r), id); err != nil {
		fail(w, err)
		return
	}
	ps, err := a.store.ListProspects(r.Context(), orgID(r), store.ProspectFilter{AgentID: id, Stage: domain.Stage(r.URL.Query().Get("stage"))})
	if err != nil {
		fail(w, err)
		return
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Score > ps[j].Score })
	writeJSON(w, http.StatusOK, ps)
}

func (a *API) getProspect(w http.ResponseWriter, r *http.Request) {
	p, err := a.store.GetProspect(r.Context(), orgID(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	ms, err := a.store.ListMessages(r.Context(), orgID(r), store.MessageFilter{ProspectID: p.ID})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"prospect": p, "messages": ms})
}

func (a *API) researchOne(w http.ResponseWriter, r *http.Request) {
	p, err := a.sales.ResearchProspect(r.Context(), orgID(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// requireBrain checks the agent exists and has a strategy, for actions that queue background work.
func (a *API) requireBrain(w http.ResponseWriter, r *http.Request) (domain.Agent, bool) {
	ag, err := a.store.GetAgent(r.Context(), orgID(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return ag, false
	}
	if ag.Brain == nil {
		writeErr(w, http.StatusConflict, "generate the sales brain first")
		return ag, false
	}
	return ag, true
}

// researchAll queues research for every un-researched prospect of an agent.
func (a *API) researchAll(w http.ResponseWriter, r *http.Request) {
	ag, ok := a.requireBrain(w, r)
	if !ok {
		return
	}
	job, err := a.jobs.Research(r.Context(), orgID(r), ag.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// discover queues a search for new prospects in an approved source.
func (a *API) discover(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Source   string `json:"source"`
		Industry string `json:"industry"`
		Location string `json:"location"`
		OSMTag   string `json:"osm_tag"`
		Limit    int    `json:"limit"`
	}
	if !decode(w, r, &in) {
		return
	}
	ag, err := a.store.GetAgent(r.Context(), orgID(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if strings.TrimSpace(in.Industry) == "" && in.OSMTag == "" || strings.TrimSpace(in.Location) == "" {
		writeErr(w, http.StatusBadRequest, "industry and location are required")
		return
	}
	job, err := a.jobs.Discover(r.Context(), orgID(r), ag.ID, sales.DiscoverInput{Source: in.Source, Industry: in.Industry, Location: in.Location, OSMTag: in.OSMTag, Limit: in.Limit})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// runAutopilot queues one autopilot step now (the scheduler does this on its own for autopilot agents).
func (a *API) runAutopilot(w http.ResponseWriter, r *http.Request) {
	ag, ok := a.requireBrain(w, r)
	if !ok {
		return
	}
	if ag.Status != domain.AgentActive {
		writeErr(w, http.StatusConflict, "launch the agent first")
		return
	}
	job, _, err := a.jobs.Autopilot(r.Context(), orgID(r), ag.ID, 0)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	j, ok := a.queue.Get(r.Context(), orgID(r), r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// ---- Outreach, conversations, deals ------------------------------------------

func (a *API) outreach(w http.ResponseWriter, r *http.Request) {
	m, err := a.sales.DraftOutreach(r.Context(), orgID(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (a *API) reply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &in) {
		return
	}
	res, err := a.sales.HandleReply(r.Context(), orgID(r), r.PathValue("id"), in.Body)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) bookMeeting(w http.ResponseWriter, r *http.Request) {
	var in struct {
		At time.Time `json:"at"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := a.sales.BookMeeting(r.Context(), orgID(r), r.PathValue("id"), in.At)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) convert(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Value *float64 `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := a.sales.Convert(r.Context(), orgID(r), r.PathValue("id"), in.Value)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) setDeal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Value float64 `json:"value"`
	}
	if !decode(w, r, &in) {
		return
	}
	p, err := a.sales.SetDealValue(r.Context(), orgID(r), r.PathValue("id"), in.Value)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) markLost(w http.ResponseWriter, r *http.Request) {
	p, err := a.sales.MarkLost(r.Context(), orgID(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *API) runFollowUps(w http.ResponseWriter, r *http.Request) {
	ms, err := a.sales.RunFollowUps(r.Context(), orgID(r), r.PathValue("id"), 0)
	if err != nil {
		fail(w, err)
		return
	}
	if ms == nil {
		ms = []domain.Message{}
	}
	writeJSON(w, http.StatusOK, ms)
}

func (a *API) listMessages(w http.ResponseWriter, r *http.Request) {
	ms, err := a.store.ListMessages(r.Context(), orgID(r), store.MessageFilter{Status: domain.MessageStatus(r.URL.Query().Get("status"))})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ms)
}

func (a *API) reviewMessage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Approve bool   `json:"approve"`
		Body    string `json:"body"` // optional edit before sending
	}
	if !decode(w, r, &in) {
		return
	}
	m, err := a.sales.ReviewMessage(r.Context(), orgID(r), r.PathValue("id"), in.Approve, in.Body)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}
