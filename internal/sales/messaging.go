package sales

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hussein/ai-salesperson/internal/ai"
	"github.com/hussein/ai-salesperson/internal/channels"
	"github.com/hussein/ai-salesperson/internal/domain"
	"github.com/hussein/ai-salesperson/internal/integrations"
	"github.com/hussein/ai-salesperson/internal/store"
)

// ---- Outreach ---------------------------------------------------------------

// DraftOutreach writes a personalized first message. With approval required
// (the default) it waits for a human; otherwise it is sent immediately.
func (s *Service) DraftOutreach(ctx context.Context, orgID, prospectID string) (domain.Message, error) {
	org, a, p, err := s.loadAll(ctx, orgID, prospectID)
	if err != nil {
		return domain.Message{}, err
	}
	if err := s.requireEntitled(ctx, org); err != nil {
		return domain.Message{}, err
	}
	if a.Brain == nil {
		return domain.Message{}, fmt.Errorf("%w: generate the sales brain first", ErrPrecondition)
	}
	if p.Stage != domain.StageNew && p.Stage != domain.StageResearched {
		return domain.Message{}, fmt.Errorf("%w: prospect is already in stage %q", ErrPrecondition, p.Stage)
	}
	return s.draft(ctx, org, a, p, "initial", "Write the first outreach message.")
}

func (s *Service) draft(ctx context.Context, org domain.Org, a domain.Agent, p domain.Prospect, kind, instruction string) (domain.Message, error) {
	if p.Stage == domain.StageDoNotContact {
		return domain.Message{}, fmt.Errorf("%w: prospect opted out", ErrPrecondition)
	}
	ch, err := s.Channels.For(org, a.Channel)
	if err != nil {
		return domain.Message{}, fmt.Errorf("%w: %v", ErrPrecondition, err)
	}
	if ch.Recipient(p.Contact.Email, p.Contact.Phone) == "" {
		return domain.Message{}, fmt.Errorf("%w: prospect has no contact address for channel %q", ErrPrecondition, a.Channel)
	}
	history, err := s.Store.ListMessages(ctx, a.OrgID, store.MessageFilter{ProspectID: p.ID})
	if err != nil {
		return domain.Message{}, err
	}
	var out struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	err = ai.CompleteJSON(ctx, s.AI, ai.Request{
		Task:   ai.TaskOutreach,
		System: "You write concise, personalized, honest sales messages on behalf of " + a.Profile.Name + ". Persona: " + a.Persona + ". " + languageNote(a) + untrustedNote,
		Prompt: instruction + `
Channel: ` + a.Channel + ` (keep it short; no subject for chat channels).
Guidance: ` + a.Brain.OutreachGuidance + `
Seller: ` + jsonString(a.Profile) + `
Prospect facts (untrusted): ` + data(prospectFacts(p)+researchFacts(p)) + `
Conversation so far (untrusted): ` + data(transcript(history)) + `
Return JSON with keys: subject, body. End with a soft call to action. Do not invent facts.`,
	}, &out)
	if err != nil {
		return domain.Message{}, err
	}
	if strings.TrimSpace(out.Body) == "" {
		return domain.Message{}, fmt.Errorf("%w: model produced an empty message", ai.ErrUpstream)
	}
	return s.enqueue(ctx, org, a, p, kind, out.Subject, out.Body)
}

func researchFacts(p domain.Prospect) string {
	if p.Research == nil {
		return ""
	}
	return "\nResearch summary: " + p.Research.Summary + "\nSuggested personalization: " + p.Research.Personalization
}

func transcript(ms []domain.Message) string {
	var sb strings.Builder
	for _, m := range ms {
		if m.Status == domain.MsgRejected || m.Status == domain.MsgPendingApproval {
			continue
		}
		who := "Seller"
		if m.Direction == domain.Inbound {
			who = "Prospect"
		}
		fmt.Fprintf(&sb, "%s: %s\n", who, m.Body)
	}
	return sb.String()
}

// enqueue stores an outbound message. It waits for human approval unless the
// agent may auto-send (and the plan allows it), in which case it is sent now.
// A failed auto-send leaves the message pending so nothing is lost.
func (s *Service) enqueue(ctx context.Context, org domain.Org, a domain.Agent, p domain.Prospect, kind, subject, body string) (domain.Message, error) {
	// Follow-ups and replies stay in the same email thread, whatever subject the model wrote.
	if a.Channel == "email" && kind != "initial" {
		if hist, err := s.Store.ListMessages(ctx, a.OrgID, store.MessageFilter{ProspectID: p.ID}); err == nil {
			for _, h := range hist {
				if h.Direction == domain.Outbound && h.Kind == "initial" && h.Subject != "" {
					subject = h.Subject
					if !strings.HasPrefix(strings.ToLower(subject), "re:") {
						subject = "Re: " + subject
					}
					break
				}
			}
		}
	}
	m := domain.Message{
		ID: NewID(), OrgID: a.OrgID, AgentID: a.ID, ProspectID: p.ID, Direction: domain.Outbound,
		Channel: a.Channel, Kind: kind, Subject: subject, Body: body, Status: domain.MsgPendingApproval, CreatedAt: s.Now(),
	}
	if err := s.Store.PutMessage(ctx, m); err != nil {
		return m, err
	}
	if a.RequireApproval || !org.Plan.Limits().AutoSend {
		return m, nil
	}
	sent, err := s.dispatch(ctx, org, a, p, m)
	if err != nil {
		logf("auto-send of %s left for review: %v", m.ID, err)
		if sent.ID != "" {
			return sent, nil
		}
	}
	return sent, nil
}

// ReviewMessage approves (and sends) or rejects a pending message. A message
// that failed to send can be approved again to retry once the cause is fixed.
func (s *Service) ReviewMessage(ctx context.Context, orgID, messageID string, approve bool, editedBody string) (domain.Message, error) {
	m, err := s.Store.GetMessage(ctx, orgID, messageID)
	if err != nil {
		return m, err
	}
	if m.Status != domain.MsgPendingApproval && m.Status != domain.MsgFailed {
		return m, fmt.Errorf("%w: message is %s, not pending approval", ErrPrecondition, m.Status)
	}
	if !approve {
		m.Status = domain.MsgRejected
		return m, s.Store.PutMessage(ctx, m)
	}
	if strings.TrimSpace(editedBody) != "" {
		m.Body = editedBody
	}
	org, a, p, err := s.loadAll(ctx, orgID, m.ProspectID)
	if err != nil {
		return m, err
	}
	return s.dispatch(ctx, org, a, p, m)
}

// footer is the opt-out notice appended to commercial email. It is added at
// send time and not stored, so the approval screen shows only the message.
func emailFooter(org domain.Org) string {
	f := "\n\n--\nNot interested? Just reply \"stop\" and we won't contact you again."
	if org.Settings.SenderAddress != "" {
		f += "\n" + org.Settings.SenderAddress
	}
	return f
}

// dispatch is the single place messages leave the system. It re-checks opt-out,
// entitlement and the daily send limit, so a prospect who unsubscribes after a
// draft was written is never contacted and a lapsed account never sends. When
// a check fails, the message stays pending for later.
func (s *Service) dispatch(ctx context.Context, org domain.Org, a domain.Agent, p domain.Prospect, m domain.Message) (domain.Message, error) {
	fail := func(err error) (domain.Message, error) {
		m.Status, m.Error = domain.MsgFailed, err.Error()
		_ = s.Store.PutMessage(ctx, m)
		return m, fmt.Errorf("%w: %v", ErrDelivery, err)
	}
	if p.Stage == domain.StageDoNotContact {
		m.Status, m.Error = domain.MsgRejected, "prospect opted out"
		_ = s.Store.PutMessage(ctx, m)
		return m, fmt.Errorf("%w: prospect opted out", ErrPrecondition)
	}
	if err := s.requireEntitled(ctx, org); err != nil {
		return m, err
	}
	sent, err := s.Store.CountSent(ctx, org.ID, a.ID, s.Now().Add(-24*time.Hour))
	if err != nil {
		return m, err
	}
	if limit := sendLimit(org, a); sent >= limit {
		return m, fmt.Errorf("%w: daily send limit reached (%d messages per 24h); it will go out once there is room", ErrLimit, limit)
	}
	ch, err := s.Channels.For(org, m.Channel)
	if err != nil {
		return m, fmt.Errorf("%w: %v", ErrPrecondition, err)
	}
	to := ch.Recipient(p.Contact.Email, p.Contact.Phone)
	if to == "" {
		return fail(fmt.Errorf("prospect has no contact address for channel %q", m.Channel))
	}
	out := channels.Outgoing{To: to, Subject: m.Subject, Body: m.Body}
	if m.Channel == "email" && (m.Kind == "initial" || m.Kind == "followup") {
		out.Body += emailFooter(org)
		out.UnsubscribeURL = s.UnsubscribeURL(org, p.ID)
	}
	if hist, err := s.Store.ListMessages(ctx, org.ID, store.MessageFilter{ProspectID: p.ID}); err == nil {
		for _, h := range hist {
			if h.Direction == domain.Inbound && s.Now().Sub(h.CreatedAt) < 24*time.Hour {
				out.InWindow = true
			}
		}
	}
	if err := ch.Send(ctx, out); err != nil {
		return fail(err)
	}
	now := s.Now()
	m.Status, m.SentAt, m.Error = domain.MsgSent, &now, ""
	if err := s.Store.PutMessage(ctx, m); err != nil {
		return m, err
	}
	if m.Kind != "reply" && (p.Stage == domain.StageNew || p.Stage == domain.StageResearched) {
		p.Stage, p.UpdatedAt = domain.StageContacted, now
		return m, s.Store.PutProspect(ctx, p)
	}
	return m, nil
}

// ---- Conversations ----------------------------------------------------------

type ReplyResult struct {
	Prospect domain.Prospect `json:"prospect"`
	Inbound  domain.Message  `json:"inbound"`
	Intent   string          `json:"intent"`
	Response *domain.Message `json:"response,omitempty"`
}

// stageRank orders the funnel so a reply can only move a prospect forward.
var stageRank = map[domain.Stage]int{
	domain.StageNew: 0, domain.StageResearched: 1, domain.StageContacted: 2,
	domain.StageEngaged: 3, domain.StageQualified: 4, domain.StageMeetingBooked: 5, domain.StageWon: 6,
}

func advance(p *domain.Prospect, to domain.Stage) {
	switch {
	case p.Stage == domain.StageDoNotContact:
		// opt-out is permanent
	case p.Stage == domain.StageLost:
		p.Stage = to // a lost prospect who writes back is re-engaged
	case stageRank[to] > stageRank[p.Stage]:
		p.Stage = to
	}
}

const maxReplyChars = 8000

// HandleReply records a reply typed in by a user (or arriving through the API).
func (s *Service) HandleReply(ctx context.Context, orgID, prospectID, body string) (ReplyResult, error) {
	return s.handleReply(ctx, orgID, prospectID, "", body)
}

// handleReply records an inbound message, classifies it (intent, objection,
// qualification) and drafts the next response. Opt-outs are handled first and
// without the AI.
func (s *Service) handleReply(ctx context.Context, orgID, prospectID, channel, body string) (ReplyResult, error) {
	var res ReplyResult
	body = strings.TrimSpace(body)
	if body == "" {
		return res, fmt.Errorf("%w: body is required", ErrInvalid)
	}
	if len([]rune(body)) > maxReplyChars {
		body = string([]rune(body)[:maxReplyChars])
	}
	org, a, p, err := s.loadAll(ctx, orgID, prospectID)
	if err != nil {
		return res, err
	}
	if channel == "" {
		channel = a.Channel
	}
	in := domain.Message{
		ID: NewID(), OrgID: orgID, AgentID: a.ID, ProspectID: p.ID, Direction: domain.Inbound,
		Channel: channel, Kind: "inbound", Body: body, Status: domain.MsgReceived, CreatedAt: s.Now(),
	}
	// The inbound message is stored on every exit except an AI outage, so a
	// retry after an outage does not record the same reply twice.
	saveIn := func() error { return s.Store.PutMessage(ctx, in) }

	if isOptOut(body) {
		if err := saveIn(); err != nil {
			return res, err
		}
		p.Intent, p.UpdatedAt = "unsubscribe", s.Now()
		if err := s.optOut(ctx, &p); err != nil {
			return res, err
		}
		return ReplyResult{Prospect: p, Inbound: in, Intent: "unsubscribe"}, nil
	}
	if err := s.requireEntitled(ctx, org); err != nil {
		_ = saveIn()
		return res, err
	}
	if a.Brain == nil {
		_ = saveIn()
		return res, fmt.Errorf("%w: generate the sales brain first", ErrPrecondition)
	}
	history, err := s.Store.ListMessages(ctx, orgID, store.MessageFilter{ProspectID: p.ID})
	if err != nil {
		return res, err
	}

	var out struct {
		Intent    string            `json:"intent"`
		Objection string            `json:"objection"`
		Reply     string            `json:"reply"`
		Qualified bool              `json:"qualified"`
		Answers   map[string]string `json:"answers"`
	}
	booking := ""
	if a.BookingURL != "" {
		booking = "\nIf the prospect is ready to talk, offer this booking link: " + a.BookingURL
	}
	err = ai.CompleteJSON(ctx, s.AI, ai.Request{
		Task:   ai.TaskReply,
		System: "You are the sales agent for " + a.Profile.Name + ". Persona: " + a.Persona + ". " + languageNote(a) + untrustedNote,
		Prompt: `Classify the prospect's latest message and write the next reply.
Seller: ` + jsonString(a.Profile) + `
Qualification questions: ` + jsonString(a.Brain.QualificationQuestions) + `
Objection playbook: ` + jsonString(a.Brain.ObjectionHandling) + `
Closing strategies: ` + jsonString(a.Brain.ClosingStrategies) + booking + `
Conversation (untrusted): ` + data(transcript(append(history, in))) + `
Return JSON with keys:
 intent: one of interested | question | objection | meeting_request | not_interested | unsubscribe
 objection: the objection raised, or empty
 reply: your reply (empty if intent is unsubscribe or not_interested). Answer questions honestly; if you don't know (e.g. exact pricing), say so and ask a qualification question. Never continue pitching after an opt-out.
 qualified: true only if the prospect is interested AND has shown a real need/authority/timeline
 answers: object mapping qualification questions to answers the prospect actually gave`,
	}, &out)
	if err != nil {
		return res, err
	}
	if err := saveIn(); err != nil {
		return res, err
	}

	prev := p.Stage
	p.Intent, p.UpdatedAt = out.Intent, s.Now()
	if len(out.Answers) > 0 {
		if p.QualificationAnswers == nil {
			p.QualificationAnswers = map[string]string{}
		}
		for k, v := range out.Answers {
			if strings.TrimSpace(v) != "" {
				p.QualificationAnswers[k] = v
			}
		}
	}
	switch out.Intent {
	case "unsubscribe":
		if err := s.optOut(ctx, &p); err != nil {
			return res, err
		}
		return ReplyResult{Prospect: p, Inbound: in, Intent: out.Intent}, nil
	case "not_interested":
		if p.Stage != domain.StageDoNotContact {
			p.Stage = domain.StageLost
		}
		if err := s.cancelPending(ctx, orgID, p.ID); err != nil {
			return res, err
		}
	case "meeting_request":
		advance(&p, domain.StageQualified)
	default:
		advance(&p, domain.StageEngaged)
		if out.Qualified {
			advance(&p, domain.StageQualified)
		}
	}
	if stageRank[p.Stage] >= stageRank[domain.StageQualified] && stageRank[prev] < stageRank[domain.StageQualified] {
		s.onQualified(ctx, org, a, &p)
	}
	if err := s.Store.PutProspect(ctx, p); err != nil {
		return res, err
	}
	res = ReplyResult{Prospect: p, Inbound: in, Intent: out.Intent}
	if strings.TrimSpace(out.Reply) != "" && p.Stage != domain.StageDoNotContact && p.Stage != domain.StageLost {
		m, err := s.enqueue(ctx, org, a, p, "reply", "", out.Reply)
		if err != nil {
			return res, err
		}
		res.Response = &m
	}
	return res, nil
}

// optOut permanently stops contact and cancels anything waiting to be sent.
func (s *Service) optOut(ctx context.Context, p *domain.Prospect) error {
	p.Stage, p.UpdatedAt = domain.StageDoNotContact, s.Now()
	if err := s.cancelPending(ctx, p.OrgID, p.ID); err != nil {
		return err
	}
	return s.Store.PutProspect(ctx, *p)
}

// cancelPending rejects drafts that must no longer be sent (opt-out / lost / won).
func (s *Service) cancelPending(ctx context.Context, orgID, prospectID string) error {
	ms, err := s.Store.ListMessages(ctx, orgID, store.MessageFilter{ProspectID: prospectID, Status: domain.MsgPendingApproval})
	if err != nil {
		return err
	}
	for _, m := range ms {
		m.Status = domain.MsgRejected
		if err := s.Store.PutMessage(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// ---- Hand-off: qualified leads, meetings, deals -------------------------------

// onQualified runs once when a prospect first becomes a qualified lead: it
// fixes the deal value, alerts the customer's sales team by email, and pushes
// the lead to their CRM. Failures are logged and never block the pipeline.
func (s *Service) onQualified(ctx context.Context, org domain.Org, a domain.Agent, p *domain.Prospect) {
	if p.DealValue <= 0 {
		p.DealValue = a.DefaultDealValue
	}
	subject := "New qualified lead: " + p.Name
	body := fmt.Sprintf("%s is now a qualified lead for %q.\n\nContact: %s %s %s\nScore: %d/100\nIntent: %s\n",
		p.Name, a.Name, p.ContactName, p.Contact.Email, p.Contact.Phone, p.Score, p.Intent)
	for q, ans := range p.QualificationAnswers {
		body += fmt.Sprintf("\n%s\n  → %s\n", q, ans)
	}
	s.notifyTeam(ctx, org, subject, body)
	s.integrate(ctx, org, a, p, integrations.LeadQualified)
}

func (s *Service) notifyTeam(ctx context.Context, org domain.Org, subject, body string) {
	if len(org.Settings.NotifyEmails) == 0 {
		return
	}
	ch, err := s.Channels.For(org, "email")
	if err != nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, to := range org.Settings.NotifyEmails {
		if err := ch.Send(cctx, channels.Outgoing{To: to, Subject: subject, Body: body}); err != nil {
			logf("notify %s: %v", to, err)
		}
	}
}

func (s *Service) integrate(ctx context.Context, org domain.Org, a domain.Agent, p *domain.Prospect, ev integrations.Event) {
	in := org.Settings.Integrations
	if s.CRM == nil || !org.Plan.Limits().Integrations || (in.WebhookURL == "" && in.HubSpotToken == "") {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	id, err := s.CRM.Push(cctx, org, a, *p, ev)
	if err != nil {
		logf("crm push (%s) for %s: %v", ev, p.ID, err)
	}
	if id != "" {
		p.CRMID = id
	}
}

// BookMeeting records a meeting (the hand-off to the human sales team), tells
// the team, and drafts a confirmation to the prospect.
func (s *Service) BookMeeting(ctx context.Context, orgID, prospectID string, at time.Time) (domain.Prospect, error) {
	org, a, p, err := s.loadAll(ctx, orgID, prospectID)
	if err != nil {
		return p, err
	}
	if !at.After(s.Now()) {
		return p, fmt.Errorf("%w: meeting time must be in the future", ErrInvalid)
	}
	if p.Stage != domain.StageQualified && p.Stage != domain.StageEngaged && p.Stage != domain.StageMeetingBooked {
		return p, fmt.Errorf("%w: prospect is %q; only engaged or qualified prospects can be booked", ErrPrecondition, p.Stage)
	}
	prev := p.Stage
	p.Stage, p.MeetingAt, p.UpdatedAt = domain.StageMeetingBooked, &at, s.Now()
	if stageRank[prev] < stageRank[domain.StageQualified] {
		s.onQualified(ctx, org, a, &p)
	}
	s.integrate(ctx, org, a, &p, integrations.MeetingBooked)
	if err := s.Store.PutProspect(ctx, p); err != nil {
		return p, err
	}
	when := at.UTC().Format("Monday, 2 January 2006 at 15:04 UTC")
	s.notifyTeam(ctx, org, "Meeting booked: "+p.Name, fmt.Sprintf("A meeting with %s is booked for %s.", p.Name, when))
	if s.requireEntitled(ctx, org) == nil {
		name := p.ContactName
		if name == "" {
			name = p.Name
		}
		if _, err := s.enqueue(ctx, org, a, p, "reply", "", fmt.Sprintf("Hi %s,\n\nConfirming our meeting on %s.\n\nLooking forward to speaking with you.", name, when)); err != nil {
			logf("meeting confirmation for %s: %v", p.ID, err)
		}
	}
	return p, nil
}

// Convert marks a prospect as a won deal (conversion). value, if given,
// replaces the estimated deal value with the real one.
func (s *Service) Convert(ctx context.Context, orgID, prospectID string, value *float64) (domain.Prospect, error) {
	org, a, p, err := s.loadAll(ctx, orgID, prospectID)
	if err != nil {
		return p, err
	}
	if p.Stage == domain.StageDoNotContact || p.Stage == domain.StageLost {
		return p, fmt.Errorf("%w: prospect is %q", ErrPrecondition, p.Stage)
	}
	if value != nil {
		if *value < 0 {
			return p, fmt.Errorf("%w: value cannot be negative", ErrInvalid)
		}
		p.DealValue = *value
	}
	now := s.Now()
	p.Stage, p.WonAt, p.UpdatedAt = domain.StageWon, &now, now
	if err := s.cancelPending(ctx, orgID, p.ID); err != nil {
		return p, err
	}
	s.integrate(ctx, org, a, &p, integrations.DealWon)
	return p, s.Store.PutProspect(ctx, p)
}

// SetDealValue sets the estimated (or final) value of a prospect's deal.
func (s *Service) SetDealValue(ctx context.Context, orgID, prospectID string, value float64) (domain.Prospect, error) {
	p, err := s.Store.GetProspect(ctx, orgID, prospectID)
	if err != nil {
		return p, err
	}
	if value < 0 {
		return p, fmt.Errorf("%w: value cannot be negative", ErrInvalid)
	}
	p.DealValue, p.UpdatedAt = value, s.Now()
	return p, s.Store.PutProspect(ctx, p)
}

// MarkLost closes a prospect without a deal.
func (s *Service) MarkLost(ctx context.Context, orgID, prospectID string) (domain.Prospect, error) {
	p, err := s.Store.GetProspect(ctx, orgID, prospectID)
	if err != nil {
		return p, err
	}
	if p.Stage == domain.StageDoNotContact || p.Stage == domain.StageWon {
		return p, fmt.Errorf("%w: prospect is %q", ErrPrecondition, p.Stage)
	}
	p.Stage, p.UpdatedAt = domain.StageLost, s.Now()
	if err := s.cancelPending(ctx, orgID, p.ID); err != nil {
		return p, err
	}
	return p, s.Store.PutProspect(ctx, p)
}

// ---- Follow-ups -------------------------------------------------------------

// RunFollowUps drafts the next follow-up for contacted prospects that have not
// replied and are due according to the agent's follow-up sequence. max > 0
// caps how many drafts are written.
func (s *Service) RunFollowUps(ctx context.Context, orgID, agentID string, max int) ([]domain.Message, error) {
	org, err := s.entitledOrg(ctx, orgID)
	if err != nil {
		return nil, err
	}
	a, err := s.Store.GetAgent(ctx, orgID, agentID)
	if err != nil {
		return nil, err
	}
	if a.Brain == nil {
		return nil, fmt.Errorf("%w: generate the sales brain first", ErrPrecondition)
	}
	if a.Status != domain.AgentActive {
		return nil, fmt.Errorf("%w: agent is not active", ErrPrecondition)
	}
	ps, err := s.Store.ListProspects(ctx, orgID, store.ProspectFilter{AgentID: agentID, Stage: domain.StageContacted})
	if err != nil {
		return nil, err
	}
	var out []domain.Message
	for _, p := range ps {
		if max > 0 && len(out) >= max {
			break
		}
		ms, err := s.Store.ListMessages(ctx, orgID, store.MessageFilter{ProspectID: p.ID})
		if err != nil {
			return out, err
		}
		step, ok := s.dueStep(a.Brain.FollowUpSequence, ms)
		if !ok {
			continue
		}
		m, err := s.draft(ctx, org, a, p, "followup", "Write a short follow-up. Angle: "+step.Angle+". Do not repeat earlier messages.")
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, nil
}

// dueStep picks the next follow-up step, or false if none is due yet, one is
// already waiting for approval, or the sequence is finished.
func (s *Service) dueStep(seq []domain.FollowUp, ms []domain.Message) (domain.FollowUp, bool) {
	sent := 0
	var last time.Time
	for _, m := range ms {
		if m.Direction != domain.Outbound {
			continue
		}
		if m.Status == domain.MsgPendingApproval {
			return domain.FollowUp{}, false
		}
		if m.Status == domain.MsgSent && m.Kind != "reply" && m.SentAt != nil {
			sent++
			if m.SentAt.After(last) {
				last = *m.SentAt
			}
		}
	}
	if sent == 0 || sent-1 >= len(seq) {
		return domain.FollowUp{}, false
	}
	step := seq[sent-1]
	if s.Now().Before(last.Add(time.Duration(step.AfterDays) * 24 * time.Hour)) {
		return domain.FollowUp{}, false
	}
	return step, true
}
