"use client";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { api } from "@/lib/api";
import { errMsg, useAction } from "@/lib/hooks";
import { Badge, Card, ErrorBanner, PageHead, fmtDate } from "@/components/ui";
import type { Effort, IntelligenceReport, Severity } from "@/lib/types";

const SEVERITY_TONE: Record<Severity, "bad" | "warn" | "neutral"> = { high: "bad", medium: "warn", low: "neutral" };
const EFFORT_LABEL: Record<Effort, string> = { low: "Low effort", medium: "Medium effort", high: "High effort" };

export default function IntelligenceReportPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const { busy, error, run } = useAction();
  const [rep, setRep] = useState<IntelligenceReport | null>(null);
  const [loadErr, setLoadErr] = useState("");
  const alive = useRef(true);
  useEffect(() => () => { alive.current = false; }, []);

  // Poll the report itself (not a job) until it reaches a terminal state —
  // this works whether we arrived right after starting an analysis or via a
  // bookmark/refresh, where no job id is known.
  useEffect(() => {
    let cancelled = false;
    async function poll() {
      try {
        const r = await api.report(id);
        if (cancelled || !alive.current) return;
        setRep(r);
        setLoadErr("");
        if (r.status === "running") setTimeout(poll, 1500);
      } catch (e) {
        if (!cancelled && alive.current) setLoadErr(errMsg(e));
      }
    }
    void poll();
    return () => { cancelled = true; };
  }, [id]);

  if (loadErr) return <ErrorBanner message={loadErr} />;
  if (!rep) return <p className="muted">Loading…</p>;

  async function del() {
    if (!confirm(`Delete the report for "${rep!.company_name}"? This can't be undone.`)) return;
    // deleteReport resolves to undefined on success too (204 No Content has no
    // body), so a plain truthiness check on its result can't tell success from
    // failure — return an explicit sentinel instead.
    const ok = await run("delete", async () => { await api.deleteReport(id); return true; });
    if (ok) router.push("/intelligence");
  }

  return (
    <>
      <PageHead title={rep.company_name} subtitle={rep.website}
        actions={<>
          <Link className="btn btn-sm" href="/intelligence">← All reports</Link>
          {rep.status !== "running" && <button className="btn btn-sm btn-danger" disabled={!!busy} onClick={del}>{busy === "delete" ? "Deleting…" : "Delete"}</button>}
        </>} />
      <ErrorBanner message={error} />

      {rep.status === "running" && (
        <Card><div className="row"><Badge tone="warn">Researching…</Badge><span className="muted small">Searching the web, identifying competitors, and writing recommendations — this takes a minute.</span></div></Card>
      )}
      {rep.status === "failed" && (
        <Card><div className="banner banner-error" style={{ marginBottom: 0 }}>Couldn't complete this analysis: {rep.error || "unknown error"}.</div></Card>
      )}

      {rep.status === "done" && (
        <>
          <Card title="Company profile">
            <p style={{ marginTop: 0 }}>{rep.profile.summary}</p>
            {(rep.profile.industry || rep.profile.target_market || rep.profile.company_size || rep.profile.founded_year) && (
              <dl className="small" style={{ margin: "12px 0", display: "grid", gridTemplateColumns: "auto 1fr", gap: "6px 14px" }}>
                {rep.profile.industry && <><dt className="muted">Industry</dt><dd style={{ margin: 0 }}>{rep.profile.industry}</dd></>}
                {rep.profile.target_market && <><dt className="muted">Target market</dt><dd style={{ margin: 0 }}>{rep.profile.target_market}</dd></>}
                {rep.profile.company_size && <><dt className="muted">Size</dt><dd style={{ margin: 0 }}>{rep.profile.company_size}</dd></>}
                {rep.profile.founded_year && <><dt className="muted">Founded</dt><dd style={{ margin: 0 }}>{rep.profile.founded_year}</dd></>}
                {!!rep.profile.locations?.length && <><dt className="muted">Locations</dt><dd style={{ margin: 0 }}>{rep.profile.locations.join(", ")}</dd></>}
              </dl>
            )}
            {rep.profile.value_proposition && <p className="small"><strong>Value proposition:</strong> {rep.profile.value_proposition}</p>}
            {!!rep.profile.strengths?.length && (
              <div className="grid grid-2">
                <div><h3>Strengths</h3><ul className="list">{rep.profile.strengths.map((s, i) => <li key={i}>{s}</li>)}</ul></div>
                {!!rep.profile.signals?.length && <div><h3>Recent signals</h3><ul className="list">{rep.profile.signals.map((s, i) => <li key={i}>{s}</li>)}</ul></div>}
              </div>
            )}
          </Card>

          {!!rep.competitors?.length && (
            <Card title={`Competitors (${rep.competitors.length})`}>
              <div className="grid grid-2">
                {rep.competitors.map((c, i) => (
                  <div key={i} className="card" style={{ margin: 0 }}>
                    <div className="card-head"><h2>{c.name}</h2>{c.website && <a href={c.website} target="_blank" rel="noreferrer" className="small">{c.website}</a>}</div>
                    <p className="small" style={{ marginTop: 0 }}>{c.summary}</p>
                    <h3>What they do better</h3>
                    <ul className="list">{c.advantages.map((a, j) => <li key={j}>{a}</li>)}</ul>
                  </div>
                ))}
              </div>
            </Card>
          )}

          {!!rep.gaps?.length && (
            <Card title={`Gaps (${rep.gaps.length})`}>
              <div className="stack">
                {rep.gaps.map((g, i) => (
                  <div key={i} style={{ borderBottom: i < rep.gaps!.length - 1 ? "1px solid var(--border)" : undefined, paddingBottom: 12 }}>
                    <div className="row"><strong>{g.area}</strong><Badge tone={SEVERITY_TONE[g.severity]}>{g.severity} priority</Badge></div>
                    <p className="small" style={{ margin: "4px 0" }}>{g.description}</p>
                    <p className="muted small" style={{ margin: 0 }}>Evidence: {g.evidence}</p>
                  </div>
                ))}
              </div>
            </Card>
          )}

          {!!rep.recommendations?.length && (
            <Card title="How to close the gap">
              <div className="stack">
                {rep.recommendations.map((r, i) => (
                  <div key={i} style={{ borderBottom: i < rep.recommendations!.length - 1 ? "1px solid var(--border)" : undefined, paddingBottom: 12 }}>
                    <div className="row"><Badge tone="info">#{r.priority}</Badge><strong>{r.action}</strong><Badge tone="neutral">{EFFORT_LABEL[r.effort]}</Badge></div>
                    <p className="small" style={{ margin: "4px 0 0" }}>Addresses: {r.gap_area}</p>
                    <p className="small" style={{ margin: 0 }}>Impact: {r.impact}</p>
                  </div>
                ))}
              </div>
            </Card>
          )}

          {(!rep.competitors?.length && !rep.gaps?.length) && (
            <Card><p className="muted" style={{ margin: 0 }}>No reliable competitor information was found for this company, so no gaps could be identified. Try adding a website, or a more specific company name.</p></Card>
          )}

          {!!rep.sources?.length && (
            <Card title="Sources">
              <ul className="list small">{rep.sources.map((s, i) => <li key={i}><a href={s.url} target="_blank" rel="noreferrer">{s.title || s.url}</a></li>)}</ul>
            </Card>
          )}
          <p className="muted small">Completed {fmtDate(rep.completed_at)}</p>
        </>
      )}
    </>
  );
}
