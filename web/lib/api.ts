import type {
  Agent, APIKey, BusinessProfile, Branding, CheckResult, ClientView, Job, Me, Message, OrgSettings, PlanInfo,
  PlanName, Prospect, ReplyResult, SettingsResponse, Stats, Targeting, Org,
} from "./types";

const BASE = process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";
const TOKEN_KEY = "aisp_token";
const AGENCY_KEY = "aisp_agency_token";

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
}

// Tokens live in localStorage: simple, but readable by any script on the page.
// Move to an httpOnly cookie issued by the API before production use.
const store = {
  get: (k: string) => { try { return localStorage.getItem(k); } catch { return null; } },
  set: (k: string, v: string) => { try { localStorage.setItem(k, v); } catch { /* ignore */ } },
  del: (k: string) => { try { localStorage.removeItem(k); } catch { /* ignore */ } },
};

export const token = {
  get: () => store.get(TOKEN_KEY),
  set: (t: string) => store.set(TOKEN_KEY, t),
  clear: () => { store.del(TOKEN_KEY); store.del(AGENCY_KEY); },
  /** An agency stepping into a client: remember its own session to come back to. */
  enterClient: (clientToken: string) => {
    const own = store.get(TOKEN_KEY);
    if (own && !store.get(AGENCY_KEY)) store.set(AGENCY_KEY, own);
    store.set(TOKEN_KEY, clientToken);
  },
  leaveClient: (): boolean => {
    const own = store.get(AGENCY_KEY);
    if (!own) return false;
    store.set(TOKEN_KEY, own);
    store.del(AGENCY_KEY);
    return true;
  },
};

async function request<T>(method: string, path: string, body?: unknown, contentType = "application/json"): Promise<T> {
  const headers: Record<string, string> = {};
  const t = token.get();
  if (t) headers.Authorization = `Bearer ${t}`;
  if (body !== undefined) headers["Content-Type"] = contentType;
  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method, headers,
      body: body === undefined ? undefined : typeof body === "string" ? body : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, `Cannot reach the API at ${BASE}. Is the server running?`);
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    if (res.status === 401 && t) {
      token.clear();
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
  signup: (org_name: string, email: string, password: string) =>
    post<{ token: string }>("/v1/auth/signup", { org_name, email, password }),
  login: (email: string, password: string) => post<{ token: string }>("/v1/auth/login", { email, password }),
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
