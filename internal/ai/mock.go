package ai

import (
	"context"
	"strings"
)

// Mock returns deterministic output so the service runs end to end without an
// API key. Its content is placeholder text, not real analysis.
type Mock struct{}

func (Mock) Complete(_ context.Context, r Request) (string, error) {
	switch r.Task {
	case TaskProfile:
		return `{"name":"(mock) Example Co","description":"Mock profile extracted from the website.","products":["Product A"],"services":["Service B"],"target_market":"Small and mid-size businesses","value_proposition":"Saves time and grows revenue.","faqs":[{"question":"How much does it cost?","answer":"Pricing depends on scope."}]}`, nil
	case TaskBrain:
		return `{"ideal_customer_profile":"Growing local businesses without a modern digital presence.","strategy":"Lead with a specific observation, offer one concrete benefit, propose a short call.","target_industries":["Restaurants","Retail"],"pain_points":["Manual ordering","Low online visibility"],"qualification_questions":["What do you use today?","What is your timeline?","Who makes the decision?"],"outreach_guidance":"Short, specific, friendly; reference one real fact about the prospect.","follow_up_sequence":[{"after_days":3,"angle":"Short reminder with one extra benefit"},{"after_days":7,"angle":"Relevant case study"}],"objection_handling":[{"objection":"We already have a solution","response":"Acknowledge it and ask what it does not cover today."},{"objection":"Too expensive","response":"Ask about the cost of the problem and offer a scoped starting point."}],"closing_strategies":["Offer two meeting slots","Propose a small pilot"]}`, nil
	case TaskResearch:
		return `{"summary":"(mock) Prospect matches the ideal customer profile based on the supplied notes.","needs":["Direct online ordering"],"personalization":"Mention their recent growth.","score":72,"reasons":["Matches target industry","Located in target region"]}`, nil
	case TaskOutreach:
		return `{"subject":"Quick idea for your business","body":"Hi there, I noticed your business is growing. We help companies like yours with a simple solution. Open to a 15-minute call this week?"}`, nil
	case TaskReply:
		p := strings.ToLower(lastProspectLine(r.Prompt))
		switch {
		case strings.Contains(p, "unsubscribe") || strings.Contains(p, "stop "):
			return `{"intent":"unsubscribe","objection":"","reply":"","qualified":false,"answers":{}}`, nil
		case strings.Contains(p, "how much") || strings.Contains(p, "cost") || strings.Contains(p, "price"):
			return `{"intent":"question","objection":"","reply":"Pricing depends on scope. To give you an accurate number, could you tell me roughly how many locations you have?","qualified":false,"answers":{}}`, nil
		case strings.Contains(p, "already have"):
			return `{"intent":"objection","objection":"already has a solution","reply":"That makes sense. What does your current setup not cover well today?","qualified":false,"answers":{}}`, nil
		case strings.Contains(p, "meeting") || strings.Contains(p, "call"):
			return `{"intent":"meeting_request","objection":"","reply":"Happy to. Would Tuesday or Thursday afternoon work?","qualified":true,"answers":{}}`, nil
		}
		return `{"intent":"interested","objection":"","reply":"Great to hear. What are you using today, and what would you most like to improve?","qualified":false,"answers":{}}`, nil
	case TaskIntelligence:
		return mockIntelligence(r.Prompt), nil
	}
	return "{}", nil
}

// mockIntelligence returns a clearly-labeled synthetic report so the feature
// runs end to end offline. A name containing "unknownco" exercises the
// honest "couldn't find anything" path real research can also hit.
func mockIntelligence(prompt string) string {
	if strings.Contains(strings.ToLower(prompt), "unknownco") {
		return `{"profile":{"summary":"(mock) No reliable public information was found for this company."},"competitors":[],"gaps":[],"recommendations":[],"sources":[]}`
	}
	return `{"profile":{"summary":"(mock) A growing local business with a solid core offering but a limited digital presence.","industry":"Local services","products":["Core product line"],"services":["In-person service"],"target_market":"Local consumers and small businesses","locations":["One primary location"],"founded_year":"2015","company_size":"11-50 employees","value_proposition":"Reliable, personal service.","strengths":["Strong local reputation","Experienced staff"],"signals":["Recently opened a second location"]},` +
		`"competitors":[{"name":"(mock) Rival Co","website":"https://rival.example.com","summary":"A larger regional competitor.","advantages":["Online ordering and booking","Active social media presence","Loyalty program"]},{"name":"(mock) Alt Services Inc","website":"https://alt.example.com","summary":"A newer, digitally-native entrant.","advantages":["Mobile app","Same-day fulfillment"]}],` +
		`"gaps":[{"area":"Online ordering","description":"No way for customers to order or book online; competitors offer this.","severity":"high","evidence":"Rival Co and Alt Services Inc both offer online ordering/booking; this company's site has no such option."},{"area":"Customer loyalty","description":"No loyalty or repeat-customer program found.","severity":"medium","evidence":"Rival Co runs a loyalty program; nothing comparable was found for this company."}],` +
		`"recommendations":[{"gap_area":"Online ordering","action":"Launch a simple online ordering/booking flow on the existing site.","impact":"Captures customers who currently go to competitors for convenience alone.","effort":"medium","priority":1},{"gap_area":"Customer loyalty","action":"Introduce a basic points-based loyalty program.","impact":"Increases repeat-visit rate.","effort":"low","priority":2}],` +
		`"sources":[{"title":"(mock) Company website","url":"https://example.com"},{"title":"(mock) Rival Co website","url":"https://rival.example.com"}]}`
}

// lastProspectLine returns the prospect's latest message from the transcript,
// so the mock reacts to what was said and not to the prompt's instructions.
func lastProspectLine(prompt string) string {
	i := strings.LastIndex(prompt, "Prospect: ")
	if i < 0 {
		return ""
	}
	line := prompt[i+len("Prospect: "):]
	if j := strings.Index(line, "\n"); j >= 0 {
		line = line[:j]
	}
	return line
}
