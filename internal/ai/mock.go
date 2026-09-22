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
	}
	return "{}", nil
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
