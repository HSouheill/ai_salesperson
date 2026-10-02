import type {
  Agent, APIKey, BusinessProfile, Branding, CheckResult, ClientView, Job, Me, Message, OrgSettings, PlanInfo,
  PlanName, Prospect, ReplyResult, SettingsResponse, Stats, Targeting, Org,
} from "./types";

const BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

// The session lives in an httpOnly cookie the browser sends automatically
// (credentials: "include" below) — this page's JS never sees the token value,
// so an XSS bug can't exfiltrate it. Mutating requests also need the
// double-submit CSRF header, read from the one cookie that isn't httpOnly.
function readCookie(name: string): string {
  if (typeof document === "undefined") return "";
  const m = document.cookie.match(new RegExp("(?:^|; )" + name + "=([^;]*)"));
  return m ? decodeURIComponent(m[1]) : "";
}

let unauthorizedHandled = false;

async function request<T>(method: string, path: string, body?: unknown, contentType = "application/json"): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = contentType;
  if (method !== "GET" && method !== "HEAD") {
    const csrf = readCookie("aisp_csrf");
    if (csrf) headers["X-CSRF-Token"] = csrf;
  }
  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method, headers, credentials: "include",
      body: body === undefined ? undefined : typeof body === "string" ? body : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, `Cannot reach the API at ${BASE}. Is the server running?`);
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    if (res.status === 401 && !unauthorizedHandled) {
      unauthorizedHandled = true; // avoid a redirect loop if /login itself ever 401s
      if (typeof window !== "undefined") window.location.href = "/login";
    }
    throw new ApiError(res.status, data.error ?? `Request failed (${res.status})`);
  }
  return data as T;
}

const get = <T,>(p: string) => request<T>("GET", p);
const post = <T,>(p: string, b?: unknown) => request<T>("POST", p, b ?? {});
const put = <T,>(p: string, b: unknown) => request<T>("PUT", p, b);

export interface AgentInput {
  name: string; persona?: string; channel?: string; language?: string; booking_url?: string;
  profile: BusinessProfile; targeting: Targeting; require_approval?: boolean; autopilot?: boolean;
  min_score?: number; daily_send_limit?: number; auto_discover?: boolean; discovery_source?: string;
  default_deal_value?: number;
}

export interface DiscoverInput { source?: string; industry: string; location: string; osm_tag?: string; limit?: number }
export interface ClientInput {
  name: string; slug?: string; email: string; password: string; plan: PlanName; branding: Branding;
}

export const api = {
  signup: (org_name: string, email: string, password: string, captcha_token?: string) =>
    post<{ token: string }>("/v1/auth/signup", { org_name, email, password, captcha_token }),
  login: (email: string, password: string) => post<{ token: string }>("/v1/auth/login", { email, password }),
  logout: () => request<void>("POST", "/v1/auth/logout"),
  plans: () => get<PlanInfo[]>("/v1/plans"),
  publicBranding: (slug: string) => get<Branding>(`/v1/public/branding?slug=${encodeURIComponent(slug)}`),
  me: () => get<Me>("/v1/me"),
  dashboard: () => get<Stats>("/v1/dashboard"),
  analyzeWebsite: (website: string) => post<BusinessProfile>("/v1/onboarding/analyze", { website }),
  sources: () => get<string[]>("/v1/sources"),

  settings: () => get<SettingsResponse>("/v1/settings"),
  saveSettings: (s: OrgSettings) => put<SettingsResponse>("/v1/settings", s),
  testSettings: () => post<{ smtp: CheckResult; imap: CheckResult }>("/v1/settings/test"),
  rotateInboundToken: () => post<SettingsResponse>("/v1/settings/rotate-inbound-token"),
  apiKeys: () => get<APIKey[]>("/v1/api-keys"),
  createAPIKey: (name: string) => post<{ key: string; api_key: APIKey }>("/v1/api-keys", { name }),
  deleteAPIKey: (id: string) => request<void>("DELETE", `/v1/api-keys/${id}`),

  checkout: (plan: PlanName) => post<{ url: string }>("/v1/billing/checkout", { plan }),
  portal: () => post<{ url: string }>("/v1/billing/portal"),

  clients: () => get<ClientView[]>("/v1/clients"),
  createClient: (c: ClientInput) => post<Org>("/v1/clients", c),
  updateClient: (id: string, c: { name?: string; plan?: PlanName; branding?: Branding }) => put<Org>(`/v1/clients/${id}`, c),
  clientLogin: (id: string) => post<{ token: string; org: Org }>(`/v1/clients/${id}/login`),
  exitClient: () => post<{ org: Org }>("/v1/clients/exit"),

  agents: () => get<Agent[]>("/v1/agents"),
  agent: (id: string) => get<Agent>(`/v1/agents/${id}`),
  createAgent: (a: AgentInput) => post<Agent>("/v1/agents", a),
  updateAgent: (id: string, a: AgentInput) => put<Agent>(`/v1/agents/${id}`, a),
  generateBrain: (id: string) => post<Agent>(`/v1/agents/${id}/brain`),
  setStatus: (id: string, status: Agent["status"]) => post<Agent>(`/v1/agents/${id}/status`, { status }),
  prospects: (agentId: string, stage = "") =>
    get<Prospect[]>(`/v1/agents/${agentId}/prospects${stage ? `?stage=${stage}` : ""}`),
  importProspects: (agentId: string, csv: string) =>
    request<{ added: number; skipped: number; suppressed: number }>("POST", `/v1/agents/${agentId}/prospects`, csv, "text/csv"),
  researchAll: (agentId: string) => post<Job>(`/v1/agents/${agentId}/research`),
  discover: (agentId: string, d: DiscoverInput) => post<Job>(`/v1/agents/${agentId}/discover`, d),
  runAutopilot: (agentId: string) => post<Job>(`/v1/agents/${agentId}/autopilot/run`),
  job: (id: string) => get<Job>(`/v1/jobs/${id}`),
  runFollowUps: (agentId: string) => post<Message[]>(`/v1/agents/${agentId}/followups`),

  prospect: (id: string) => get<{ prospect: Prospect; messages: Message[] }>(`/v1/prospects/${id}`),
  research: (id: string) => post<Prospect>(`/v1/prospects/${id}/research`),
  outreach: (id: string) => post<Message>(`/v1/prospects/${id}/outreach`),
  reply: (id: string, body: string) => post<ReplyResult>(`/v1/prospects/${id}/replies`, { body }),
  bookMeeting: (id: string, at: string) => post<Prospect>(`/v1/prospects/${id}/meeting`, { at }),
  setDeal: (id: string, value: number) => put<Prospect>(`/v1/prospects/${id}/deal`, { value }),
  convert: (id: string, value?: number) => post<Prospect>(`/v1/prospects/${id}/convert`, value === undefined ? {} : { value }),
  markLost: (id: string) => post<Prospect>(`/v1/prospects/${id}/lost`),

  messages: (status = "") => get<Message[]>(`/v1/messages${status ? `?status=${status}` : ""}`),
  review: (id: string, approve: boolean, body?: string) =>
    post<Message>(`/v1/messages/${id}/review`, { approve, body }),
};

/** Polls a background job until it finishes, reporting progress. */
export async function waitForJob(id: string, onProgress: (j: Job) => void, alive: () => boolean = () => true): Promise<Job> {
  for (;;) {
    const j = await api.job(id);
    if (!alive()) return j;
    onProgress(j);
    if (j.status === "done") return j;
    await new Promise((r) => setTimeout(r, 1000));
  }
}
