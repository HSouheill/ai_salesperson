// Mirrors the JSON shapes returned by the Go API (internal/domain).
export type Stage =
  | "new" | "researched" | "contacted" | "engaged" | "qualified"
  | "meeting_booked" | "won" | "lost" | "do_not_contact";

export interface FAQ { question: string; answer: string }
export interface BusinessProfile {
  website?: string; name: string; description: string;
  products?: string[]; services?: string[]; target_market?: string;
  value_proposition?: string; faqs?: FAQ[];
}
export interface Targeting {
  industries: string[]; locations: string[]; company_sizes?: string[]; goals?: string;
}
export interface Brain {
  ideal_customer_profile: string; strategy: string; target_industries: string[];
  pain_points: string[]; qualification_questions: string[]; outreach_guidance: string;
  follow_up_sequence: { after_days: number; angle: string }[];
  objection_handling: { objection: string; response: string }[];
  closing_strategies: string[]; generated_at: string;
}
export interface Agent {
  id: string; name: string; persona?: string; channel: string;
  profile: BusinessProfile; targeting: Targeting; brain?: Brain;
  status: "draft" | "active" | "paused"; require_approval: boolean; created_at: string;
  language?: string; booking_url?: string; default_deal_value: number; autopilot: boolean;
  min_score: number; daily_send_limit: number; auto_discover: boolean;
  discovery_source?: string; last_discovery_at?: string;
}
export interface Prospect {
  id: string; agent_id: string; name: string; contact_name?: string;
  contact: { email?: string; phone?: string; website?: string };
  industry?: string; location?: string; notes?: string; source: string;
  stage: Stage; score: number; score_reasons?: string[];
  research?: { summary: string; needs: string[]; personalization: string; researched_at: string };
  intent?: string; meeting_at?: string; deal_value: number; won_at?: string; crm_id?: string;
  qualification_answers?: Record<string, string>;
}
export interface Message {
  id: string; agent_id: string; prospect_id: string; direction: "outbound" | "inbound";
  channel: string; kind: string; subject?: string; body: string;
  status: "pending_approval" | "approved" | "sent" | "rejected" | "failed" | "received";
  error?: string; created_at: string; sent_at?: string;
}
export interface Stats {
  prospects_identified: number; prospects_researched: number; outreach_prepared: number;
  outreach_sent: number; awaiting_approval: number; active_conversations: number;
  qualified_leads: number; meetings_booked: number; won: number; estimated_pipeline: number;
  won_revenue: number; by_stage: Partial<Record<Stage, number>>;
}
export interface Job { id: string; kind: string; status: "queued" | "running" | "done"; total: number; done: number; failed: number; error?: string; added?: number }
export interface ReplyResult { prospect: Prospect; inbound: Message; intent: string; response?: Message }

export type PlanName = "trial" | "starter" | "growth" | "pro" | "enterprise";
export interface Limits {
  MaxAgents: number; MaxProspects: number; DailySends: number;
  AutoSend: boolean; Integrations: boolean; APIAccess: boolean; WhiteLabel: boolean;
}
export interface Branding { product_name?: string; logo_url?: string; primary_color?: string }
export interface Org {
  id: string; name: string; plan: PlanName; parent_id?: string; slug?: string; trial_ends_at?: string;
  billing: { status?: string; customer_id?: string }; branding: Branding; created_at: string;
}
export interface Me {
  org: Org; entitled: boolean; limits: Limits; agency_session: boolean; is_client: boolean; billing_available: boolean;
}
export interface PlanInfo { plan: PlanName; name: string; price_usd_month: number; highlights: string[]; limits: Limits }

export interface SMTP { host: string; port: number; username: string; password?: string; security: string }
export interface IMAP { host: string; port: number; username: string; password?: string; security: string; folder?: string }
export interface EmailSettings { from_name: string; from_address: string; smtp: SMTP; imap?: IMAP | null }
export interface WhatsAppSettings {
  phone_number_id: string; access_token?: string; app_secret?: string; verify_token?: string;
  template_name?: string; template_lang?: string;
}
export interface OrgSettings {
  email?: EmailSettings | null; whatsapp?: WhatsAppSettings | null; sender_address?: string;
  notify_emails?: string[]; google_places_key?: string;
  integrations: { webhook_url?: string; webhook_secret?: string; hubspot_token?: string };
}
export interface SettingsResponse {
  settings: OrgSettings; secrets_set: Record<string, boolean>;
  inbound_urls: { email: string; whatsapp: string }; channels: { email: boolean; whatsapp: boolean };
  integrations_allowed: boolean;
}
export interface CheckResult { configured: boolean; ok: boolean; error?: string }
export interface APIKey { id: string; name: string; prefix: string; created_at: string }
export interface ClientView { org: Org; stats: Stats }
