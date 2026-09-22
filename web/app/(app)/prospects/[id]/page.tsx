"use client";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useState } from "react";
import { api } from "@/lib/api";
import { useAction, useLoad } from "@/lib/hooks";
import PendingMessage from "@/components/Review";
import { Badge, Card, Empty, ErrorBanner, List, PageHead, ScoreBadge, StageBadge, fmtDate, money } from "@/components/ui";

export default function ProspectPage() {
  const { id } = useParams<{ id: string }>();
  const { data, error, loading, reload } = useLoad(() => api.prospect(id), [id]);
  const { busy, error: actErr, run } = useAction();
  const [reply, setReply] = useState("");
  const [meeting, setMeeting] = useState("");
  const [deal, setDeal] = useState<string | null>(null);
  const [notice, setNotice] = useState("");

  if (loading && !data) return <p className="muted">Loading…</p>;
  if (!data) return <ErrorBanner message={error} />;
  const { prospect: p, messages } = data;
  const closed = p.stage === "do_not_contact" || p.stage === "lost" || p.stage === "won";
  const canDraft = p.stage === "new" || p.stage === "researched";
  const canBook = ["engaged", "qualified", "meeting_booked"].includes(p.stage);
  const canClose = !["do_not_contact", "lost", "won", "new", "researched"].includes(p.stage);
  const done = () => { void reload(); };

  return (
    <>
      <PageHead title={p.name} subtitle={[p.industry, p.location].filter(Boolean).join(" · ") || undefined}
        actions={<><ScoreBadge score={p.score} researched={!!p.research} /><StageBadge stage={p.stage} />
          <Link className="btn btn-sm" href={`/agents/${p.agent_id}`}>← Agent</Link></>} />
      <ErrorBanner message={actErr} />
      {notice && <div className="banner banner-good" role="status">{notice}</div>}
      {p.stage === "do_not_contact" && <div className="banner banner-error">This prospect opted out. They will not be contacted again.</div>}

      <div className="grid grid-2">
        <Card title="Lead intelligence" actions={<button className="btn btn-sm" disabled={!!busy} onClick={() => run("research", () => api.research(id)).then((r) => r && done())}>{busy === "research" ? "Researching…" : p.research ? "Re-run" : "Research"}</button>}>
          {!p.research ? <Empty>Not researched yet.</Empty> : <>
            <p style={{ marginTop: 0 }}>{p.research.summary}</p>
            <h3>Why {p.score}/100</h3><List items={p.score_reasons} />
            <h3>Likely needs</h3><List items={p.research.needs} />
            {p.research.personalization && <><h3>Personalization angle</h3><p style={{ margin: 0 }}>{p.research.personalization}</p></>}
          </>}
        </Card>
        <Card title="Details">
          <dl className="small" style={{ margin: 0, display: "grid", gridTemplateColumns: "auto 1fr", gap: "6px 14px" }}>
            <dt className="muted">Contact</dt><dd style={{ margin: 0 }}>{p.contact_name ?? "—"}</dd>
            <dt className="muted">Email</dt><dd style={{ margin: 0 }}>{p.contact.email ?? "—"}</dd>
            <dt className="muted">Phone</dt><dd style={{ margin: 0 }}>{p.contact.phone ?? "—"}</dd>
            <dt className="muted">Website</dt><dd style={{ margin: 0 }}>{p.contact.website ?? "—"}</dd>
            <dt className="muted">Intent</dt><dd style={{ margin: 0 }}>{p.intent ? <Badge tone="info">{p.intent.replace("_", " ")}</Badge> : "—"}</dd>
            {p.meeting_at && <><dt className="muted">Meeting</dt><dd style={{ margin: 0 }}>{fmtDate(p.meeting_at)}</dd></>}
            <dt className="muted">Deal value</dt><dd style={{ margin: 0 }}>{p.deal_value ? money(p.deal_value) : "—"}{p.stage === "won" && p.won_at ? ` · won ${fmtDate(p.won_at)}` : ""}</dd>
            {p.crm_id && <><dt className="muted">In your CRM</dt><dd style={{ margin: 0 }}><Badge tone="good">synced · {p.crm_id}</Badge></dd></>}
          </dl>
          {p.notes && <><h3>Notes</h3><p className="small" style={{ margin: 0 }}>{p.notes}</p></>}
          {p.qualification_answers && Object.keys(p.qualification_answers).length > 0 && <>
            <h3>Qualification answers</h3>
            <List items={Object.entries(p.qualification_answers).map(([q, a]) => `${q} — ${a}`)} /></>}
        </Card>
      </div>

      <Card title="Conversation" actions={canDraft && (
        <button className="btn btn-primary btn-sm" disabled={!!busy} onClick={() => run("outreach", () => api.outreach(id)).then((r) => { if (r) { setNotice("Draft created — approve it below to send."); done(); } })}>
          {busy === "outreach" ? "Writing…" : "Draft outreach"}</button>)}>
        {messages.length === 0 ? <Empty>No messages yet.{canDraft ? " Draft a personalized first message." : ""}</Empty> : (
          <div className="thread">
            {messages.filter((m) => m.status !== "rejected").map((m) => m.status === "pending_approval" || m.status === "failed" ? (
              <div key={m.id} className="msg out pending">
                <div className="meta"><Badge tone={m.status === "failed" ? "bad" : "warn"}>{m.status === "failed" ? "Not sent" : "Awaiting your approval"}</Badge><span>{m.kind}</span></div>
                <PendingMessage m={m} onDone={done} />
              </div>
            ) : (
              <div key={m.id} className={`msg ${m.direction === "outbound" ? "out" : "in"}`}>
                <div className="meta"><strong>{m.direction === "outbound" ? "AI agent" : p.name}</strong><span>{fmtDate(m.sent_at ?? m.created_at)}</span></div>
                {m.subject && <div><strong>{m.subject}</strong></div>}{m.body}
              </div>
            ))}
          </div>
        )}
      </Card>

      {!closed && p.stage !== "new" && p.stage !== "researched" && (
        <Card title="Log a reply from this prospect">
          <p className="muted small" style={{ marginTop: 0 }}>Inbound email/WhatsApp isn't connected yet — paste their reply here. The AI classifies it, answers, and updates the pipeline.</p>
          <textarea aria-label="Prospect reply" value={reply} onChange={(e) => setReply(e.target.value)} placeholder="e.g. How much does it cost?" />
          <div style={{ marginTop: 8 }}><button className="btn btn-primary" disabled={!!busy || !reply.trim()} onClick={async () => {
            const r = await run("reply", () => api.reply(id, reply));
            if (r) { setReply(""); setNotice(`Detected intent: ${r.intent.replace("_", " ")}.${r.response ? " A reply was drafted." : ""}`); done(); }
          }}>{busy === "reply" ? "Analysing…" : "Process reply"}</button></div>
        </Card>
      )}

      {(canBook || canClose) && (
        <Card title="Deal">
          <div className="row">
            <label htmlFor="dv" className="muted small" style={{ margin: 0 }}>Deal value (USD)</label>
            <input id="dv" type="number" min={0} style={{ width: 140 }} value={deal ?? p.deal_value} onChange={(e) => setDeal(e.target.value)} />
            <button className="btn btn-sm" disabled={!!busy || deal === null} onClick={async () => { const r = await run("deal", () => api.setDeal(id, Number(deal))); if (r) { setDeal(null); setNotice("Deal value saved."); done(); } }}>Save value</button>
            {canClose && <>
              <button className="btn btn-primary btn-sm" disabled={!!busy} onClick={async () => { const r = await run("won", () => api.convert(id, deal === null ? undefined : Number(deal))); if (r) { setDeal(null); setNotice("Marked as won. 🎉"); done(); } }}>Mark as won</button>
              <button className="btn btn-danger btn-sm" disabled={!!busy} onClick={async () => { if (confirm("Close this prospect as lost?")) { const r = await run("lost", () => api.markLost(id)); if (r) done(); } }}>Mark as lost</button></>}
          </div>
        </Card>
      )}

      {canBook && (
        <Card title="Book a meeting">
          <div className="row">
            <input type="datetime-local" aria-label="Meeting time" style={{ width: "auto" }} value={meeting} onChange={(e) => setMeeting(e.target.value)} />
            <button className="btn btn-primary" disabled={!!busy || !meeting} onClick={async () => {
              const r = await run("meeting", () => api.bookMeeting(id, new Date(meeting).toISOString()));
              if (r) { setNotice("Meeting booked."); done(); }
            }}>{busy === "meeting" ? "Booking…" : "Book meeting"}</button>
          </div>
        </Card>
      )}
    </>
  );
}
