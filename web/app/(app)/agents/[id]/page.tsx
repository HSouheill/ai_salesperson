"use client";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { api, waitForJob } from "@/lib/api";
import { errMsg, useAction, useLoad } from "@/lib/hooks";
import { useMe } from "@/components/Shell";
import { Badge, Card, Empty, ErrorBanner, List, PageHead, ScoreBadge, StageBadge, STAGE_LABEL, fmtDate, money } from "@/components/ui";
import type { Agent, Stage } from "@/lib/types";

const TONE = { draft: "neutral", active: "good", paused: "warn" } as const;
const STAGES = Object.keys(STAGE_LABEL) as Stage[];
const split = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);

export default function AgentPage() {
  const { id } = useParams<{ id: string }>();
  const me = useMe();
  const agent = useLoad(() => api.agent(id), [id]);
  const [stage, setStage] = useState("");
  const prospects = useLoad(() => api.prospects(id, stage), [id, stage]);
  const { busy, error, run } = useAction();
  const [notice, setNotice] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);
  const alive = useRef(true);
  useEffect(() => () => { alive.current = false; }, []);

  const a = agent.data;
  if (agent.loading && !a) return <p className="muted">Loading…</p>;
  if (!a) return <ErrorBanner message={agent.error} />;

  async function onFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = "";
    if (!file) return;
    const r = await run("import", async () => api.importProspects(id, await file.text()));
    if (r) {
      const extra = [r.skipped ? `skipped ${r.skipped} duplicate/invalid` : "", r.suppressed ? `${r.suppressed} were already opted out and stay excluded` : ""].filter(Boolean).join("; ");
      setNotice(`Imported ${r.added} prospect${r.added === 1 ? "" : "s"}${extra ? `, ${extra}` : ""}.`);
      void prospects.reload();
    }
  }

  /** Runs a queued job and reports progress; returns the finished job. */
  async function track(key: string, start: () => Promise<{ id: string }>, label: string) {
    const job = await run(key, start);
    if (!job) return;
    setNotice(`${label}…`);
    try {
      const j = await waitForJob(job.id, (p) => setNotice(p.total ? `${label}… ${p.done}/${p.total}` : `${label}…`), () => alive.current);
      if (!alive.current) return;
      return j;
    } catch (e) { setNotice(errMsg(e)); }
  }

  async function researchAll() {
    const j = await track("research", () => api.researchAll(id), "Researching prospects");
    if (!j) return;
    setNotice(j.failed ? `Research finished: ${j.done - j.failed} succeeded, ${j.failed} failed (${j.error ?? "see logs"}).` : `Research finished for ${j.done} prospect${j.done === 1 ? "" : "s"}.`);
    void prospects.reload();
  }
  async function autopilot() {
    const j = await track("autopilot", () => api.runAutopilot(id), "Running one autopilot step");
    if (!j) return;
    setNotice(j.failed ? `Autopilot step failed: ${j.error ?? "unknown error"}` : "Autopilot step finished. New drafts (if any) are in Approvals.");
    void prospects.reload();
  }

  const setStatus = async (s: "active" | "paused") => { if (await run("status", () => api.setStatus(id, s))) void agent.reload(); };
  const brain = async () => { if (await run("brain", () => api.generateBrain(id))) void agent.reload(); };
  const followUps = async () => {
    const ms = await run("followups", () => api.runFollowUps(id));
    if (ms) setNotice(ms.length ? `${ms.length} follow-up${ms.length > 1 ? "s" : ""} drafted — review in Approvals.` : "No follow-ups are due yet.");
  };
  const b = a.brain;

  return (
    <>
      <PageHead title={a.name} subtitle={`${a.profile.name} · ${a.channel} · ${a.require_approval ? "approval required" : "auto-send"}${a.autopilot ? " · autopilot on" : ""}`}
        actions={<>
          <Badge tone={TONE[a.status]}>{a.status}</Badge>
          {a.status === "active"
            ? <button className="btn" disabled={!!busy} onClick={() => setStatus("paused")}>Pause</button>
            : <button className="btn btn-primary" disabled={!!busy || !b} title={b ? "" : "Generate the sales brain first"} onClick={() => setStatus("active")}>Launch agent</button>}
        </>} />
      <ErrorBanner message={error} />
      {notice && <div className="banner banner-good" role="status">{notice}</div>}
      {a.status === "active" && a.autopilot && (
        <div className="banner banner-good">Autopilot is on: this agent researches, writes and follows up on its own{a.require_approval ? " — messages wait for your approval" : ""}.
          {a.last_discovery_at && <> Last discovery: {fmtDate(a.last_discovery_at)}.</>}</div>
      )}

      <Card title="Prospects" actions={<>
        <select aria-label="Filter by stage" style={{ width: "auto" }} value={stage} onChange={(e) => setStage(e.target.value)}>
          <option value="">All stages</option>{STAGES.map((s) => <option key={s} value={s}>{STAGE_LABEL[s]}</option>)}
        </select>
        <input ref={fileRef} type="file" accept=".csv,text/csv" hidden onChange={onFile} />
        <button className="btn" disabled={!!busy} onClick={() => fileRef.current?.click()}>{busy === "import" ? "Importing…" : "Import CSV"}</button>
        <button className="btn" disabled={!!busy || !b} onClick={researchAll}>{busy === "research" ? "Researching…" : "Research new"}</button>
        <button className="btn" disabled={!!busy || a.status !== "active"} title={a.status !== "active" ? "Launch the agent first" : ""} onClick={followUps}>Draft follow-ups</button>
        <button className="btn" disabled={!!busy || a.status !== "active"} title={a.status !== "active" ? "Launch the agent first" : "Run one autopilot step now"} onClick={autopilot}>{busy === "autopilot" ? "Running…" : "Run autopilot now"}</button>
      </>}>
        <ErrorBanner message={prospects.error} />
        {prospects.data && prospects.data.length === 0 ? (
          <Empty>{stage ? "No prospects in this stage." : <>No prospects yet. Discover businesses below, or import a CSV with a header row and a <code>name</code> column (also: contact_name, email, phone, website, industry, location, notes, deal_value).</>}</Empty>
        ) : (
          <div className="table-wrap"><table>
            <thead><tr><th>Score</th><th>Prospect</th><th>Contact</th><th>Stage</th><th>Deal</th></tr></thead>
            <tbody>{prospects.data?.map((p) => (
              <tr key={p.id}>
                <td><ScoreBadge score={p.score} researched={!!p.research} /></td>
                <td><Link href={`/prospects/${p.id}`}>{p.name}</Link>{p.industry && <div className="muted small">{p.industry}{p.location ? ` · ${p.location}` : ""}</div>}</td>
                <td className="small">{p.contact_name}{p.contact_name && (p.contact.email || p.contact.phone) ? " · " : ""}{p.contact.email ?? p.contact.phone}</td>
                <td><StageBadge stage={p.stage} /></td>
                <td className="small">{p.deal_value ? money(p.deal_value) : "—"}</td>
              </tr>))}</tbody>
          </table></div>
        )}
      </Card>

      <Discover agent={a} disabled={!!busy || !me?.entitled} onRun={(d) => track("discover", () => api.discover(id, d), "Searching")
        .then((j) => { if (j) { setNotice(j.failed ? `Discovery failed: ${j.error}` : `Discovery finished: ${j.added ?? 0} new prospect${j.added === 1 ? "" : "s"} added.`); void prospects.reload(); } })} />

      <AgentSettings agent={a} onSaved={() => void agent.reload()} autoSendAllowed={!!me?.limits.AutoSend} />

      <Card title="Sales brain" actions={<button className="btn btn-sm" disabled={!!busy} onClick={brain}>{busy === "brain" ? "Generating…" : b ? "Regenerate" : "Generate"}</button>}>
        {!b ? <Empty>No strategy yet. Generate the sales brain to enable research, outreach and replies.</Empty> : (
          <div className="grid grid-2">
            <div><h3>Ideal customer</h3><p style={{ margin: 0 }}>{b.ideal_customer_profile}</p>
              <h3>Strategy</h3><p style={{ margin: 0 }}>{b.strategy}</p>
              <h3>Target industries</h3><List items={b.target_industries} />
              <h3>Pain points</h3><List items={b.pain_points} /></div>
            <div><h3>Qualification questions</h3><List items={b.qualification_questions} />
              <h3>Follow-up sequence</h3><List items={b.follow_up_sequence.map((f) => `After ${f.after_days} days: ${f.angle}`)} />
              <h3>Objection handling</h3><List items={b.objection_handling.map((o) => `“${o.objection}” → ${o.response}`)} />
              <h3>Closing strategies</h3><List items={b.closing_strategies} /></div>
          </div>
        )}
      </Card>
    </>
  );
}

function Discover({ agent, disabled, onRun }: { agent: Agent; disabled: boolean; onRun: (d: { source: string; industry: string; location: string; limit: number }) => Promise<unknown> }) {
  const sources = useLoad(api.sources);
  const [f, setF] = useState({ source: agent.discovery_source || "osm", industry: agent.targeting.industries[0] ?? "", location: agent.targeting.locations[0] ?? "", limit: 50 });
  return (
    <Card title="Find prospects">
      <p className="muted small" style={{ marginTop: 0 }}>Searches public business listings for companies matching your target. Nobody is contacted — found businesses are added as new prospects.</p>
      <div className="grid grid-3">
        <div className="field"><label htmlFor="ds">Source</label><select id="ds" value={f.source} onChange={(e) => setF({ ...f, source: e.target.value })}>
          {(sources.data ?? ["osm", "google_places"]).map((s) => <option key={s} value={s}>{s === "osm" ? "OpenStreetMap (free)" : s === "google_places" ? "Google Maps (your API key)" : s}</option>)}</select></div>
        <div className="field"><label htmlFor="di">Industry</label><input id="di" placeholder="restaurants" value={f.industry} onChange={(e) => setF({ ...f, industry: e.target.value })} /></div>
        <div className="field"><label htmlFor="dl">Location</label><input id="dl" placeholder="Beirut" value={f.location} onChange={(e) => setF({ ...f, location: e.target.value })} /></div>
        <div className="field"><label htmlFor="dn">How many</label><input id="dn" type="number" min={1} max={200} value={f.limit} onChange={(e) => setF({ ...f, limit: Number(e.target.value) })} /></div>
      </div>
      <button className="btn btn-primary" disabled={disabled || !f.industry.trim() || !f.location.trim()} onClick={() => onRun(f)}>Find prospects</button>
    </Card>
  );
}

function AgentSettings({ agent, onSaved, autoSendAllowed }: { agent: Agent; onSaved: () => void; autoSendAllowed: boolean }) {
  const { busy, error, run } = useAction();
  const [ok, setOk] = useState(false);
  const [f, setF] = useState({
    name: agent.name, persona: agent.persona ?? "", channel: agent.channel, language: agent.language ?? "", booking_url: agent.booking_url ?? "",
    industries: agent.targeting.industries.join(", "), locations: agent.targeting.locations.join(", "),
    require_approval: agent.require_approval, autopilot: agent.autopilot, auto_discover: agent.auto_discover,
    discovery_source: agent.discovery_source || "osm", min_score: agent.min_score, daily_send_limit: agent.daily_send_limit, deal: agent.default_deal_value,
  });
  const set = <K extends keyof typeof f>(k: K, v: (typeof f)[K]) => { setF((x) => ({ ...x, [k]: v })); setOk(false); };

  async function save() {
    const r = await run("save", () => api.updateAgent(agent.id, {
      name: f.name, persona: f.persona, channel: f.channel, language: f.language, booking_url: f.booking_url,
      profile: agent.profile, targeting: { ...agent.targeting, industries: split(f.industries), locations: split(f.locations) },
      require_approval: f.require_approval, autopilot: f.autopilot, auto_discover: f.auto_discover, discovery_source: f.discovery_source,
      min_score: Number(f.min_score), daily_send_limit: Number(f.daily_send_limit), default_deal_value: Number(f.deal),
    }));
    if (r) { setOk(true); onSaved(); }
  }

  return (
    <Card title="Agent settings" actions={<button className="btn btn-primary btn-sm" disabled={!!busy} onClick={save}>{busy ? "Saving…" : "Save"}</button>}>
      <ErrorBanner message={error} />
      {ok && <div className="banner banner-good" role="status">Saved.</div>}
      <div className="grid grid-2">
        <div className="field"><label htmlFor="an">Name</label><input id="an" value={f.name} onChange={(e) => set("name", e.target.value)} /></div>
        <div className="field"><label htmlFor="ac">Channel</label><select id="ac" value={f.channel} onChange={(e) => set("channel", e.target.value)}><option value="email">Email</option><option value="whatsapp">WhatsApp</option></select></div>
        <div className="field"><label htmlFor="ap">Personality</label><input id="ap" value={f.persona} onChange={(e) => set("persona", e.target.value)} /></div>
        <div className="field"><label htmlFor="al">Language</label><input id="al" placeholder="Match the prospect (default)" value={f.language} onChange={(e) => set("language", e.target.value)} /></div>
        <div className="field"><label htmlFor="ab">Booking link</label><input id="ab" placeholder="https://cal.com/you/intro" value={f.booking_url} onChange={(e) => set("booking_url", e.target.value)} />
          <div className="hint">Offered to interested prospects (Calendly, Cal.com, …).</div></div>
        <div className="field"><label htmlFor="ad">Default deal value (USD)</label><input id="ad" type="number" min={0} value={f.deal} onChange={(e) => set("deal", Number(e.target.value))} />
          <div className="hint">Used for the estimated pipeline until you set the real value.</div></div>
        <div className="field"><label htmlFor="ai">Target industries</label><input id="ai" value={f.industries} onChange={(e) => set("industries", e.target.value)} /></div>
        <div className="field"><label htmlFor="ao">Target locations</label><input id="ao" value={f.locations} onChange={(e) => set("locations", e.target.value)} /></div>
        <div className="field"><label htmlFor="am">Only contact prospects scoring at least</label><input id="am" type="number" min={0} max={100} value={f.min_score} onChange={(e) => set("min_score", Number(e.target.value))} /></div>
        <div className="field"><label htmlFor="at">Max messages per day (0 = plan maximum)</label><input id="at" type="number" min={0} value={f.daily_send_limit} onChange={(e) => set("daily_send_limit", Number(e.target.value))} /></div>
      </div>
      <div className="stack">
        <label className="row" style={{ fontWeight: 400 }}><input type="checkbox" style={{ width: "auto" }} checked={f.require_approval} disabled={!autoSendAllowed && f.require_approval}
          onChange={(e) => set("require_approval", e.target.checked)} /> Require my approval before any message is sent{!autoSendAllowed && <Badge tone="warn">Sending without approval needs Pro</Badge>}</label>
        <label className="row" style={{ fontWeight: 400 }}><input type="checkbox" style={{ width: "auto" }} checked={f.autopilot} onChange={(e) => set("autopilot", e.target.checked)} />
          Autopilot: research, write outreach and follow up automatically while the agent is active</label>
        <label className="row" style={{ fontWeight: 400 }}><input type="checkbox" style={{ width: "auto" }} checked={f.auto_discover} onChange={(e) => set("auto_discover", e.target.checked)} />
          Keep finding new prospects automatically (from
          <select aria-label="Discovery source" style={{ width: "auto" }} value={f.discovery_source} onChange={(e) => set("discovery_source", e.target.value)}><option value="osm">OpenStreetMap</option><option value="google_places">Google Maps</option></select>)</label>
      </div>
    </Card>
  );
}
