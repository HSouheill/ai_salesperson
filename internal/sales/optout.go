package sales

import "regexp"

// optOutRe catches clear opt-out requests without asking a model, so they are
// honoured instantly and even when the AI provider is down. It is deliberately
// conservative about single common words; the model classifies the rest
// (including other languages).
var optOutRe = regexp.MustCompile(`(?i)(^\s*(stop|unsubscribe|remove|cancel)\W*\s*$|\bunsubscribe\b|\bopt[\s-]?out\b|\bremove (me|us|my)\b|\btake (me|us) off\b|\bstop (emailing|e-mailing|contacting|messaging|texting|writing|sending)\b|\b(do not|don'?t|dont) (contact|email|message|write to|text) (me|us)\b|\bno more (emails|messages)\b|إلغاء الاشتراك|الغاء الاشتراك|لا تراسل|توقف عن|أوقف|ايقاف)`)

func isOptOut(body string) bool { return optOutRe.MatchString(body) }
