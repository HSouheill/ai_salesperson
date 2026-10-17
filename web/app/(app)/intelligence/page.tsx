"use client";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { api } from "@/lib/api";
import { useAction, useLoad } from "@/lib/hooks";
import { useMe } from "@/components/Shell";
import { Badge, Card, Empty, ErrorBanner, PageHead, fmtDate } from "@/components/ui";
import type { ReportStatus } from "@/lib/types";

const STATUS_TONE: Record<ReportStatus, "neutral" | "good" | "warn" | "bad"> = { running: "warn", done: "good", failed: "bad" };
const STATUS_LABEL: Record<ReportStatus, string> = { running: "Researching…", done: "Done", failed: "Failed" };

export default function IntelligenceList() {
  const me = useMe();
  const router = useRouter();
  const { data, error, loading } = useLoad(api.reports);
  const { busy, error: actErr, run } = useAction();
  const [name, setName] = useState("");
  const [website, setWebsite] = useState("");

  const gated = me && !me.limits.CompanyIntelligence;

  async function analyze(e: React.FormEvent) {
    e.preventDefault();
    const r = await run("analyze", () => api.analyzeCompany(name.trim(), website.trim() || undefined));
    if (r) router.push(`/intelligence/${r.report.id}`);
  }

  return (
    <>
      <PageHead title="Company Intelligence" subtitle="The AI researches any company on the live web, finds its real competitors, and tells you exactly what to fix to catch up." />
      <ErrorBanner message={actErr} />

      {gated ? (
        <Card title="Upgrade to unlock Company Intelligence">
          <p style={{ marginTop: 0 }}>Research your own business, a prospect, or a named competitor — real web search, named competitors, concrete gaps, and prioritized fixes.</p>
          <p className="muted small">Available on the Growth plan and above.{me?.org.plan === "trial" && " Your trial includes a few reports to try it first."}</p>
          <Link className="btn btn-primary" href="/billing">View plans</Link>
        </Card>
      ) : (
        <Card title="Research a company">
          <form onSubmit={analyze}>
            <div className="grid grid-2">
              <div className="field"><label htmlFor="cn">Company name</label>
                <input id="cn" placeholder="Your own business, a prospect, or a competitor" value={name} onChange={(e) => setName(e.target.value)} required /></div>
              <div className="field"><label htmlFor="cw">Website (optional, helps accuracy)</label>
                <input id="cw" placeholder="https://example.com" value={website} onChange={(e) => setWebsite(e.target.value)} /></div>
            </div>
            <button className="btn btn-primary" disabled={!!busy || !name.trim()}>{busy === "analyze" ? "Starting…" : "Analyze"}</button>
            {me && me.limits.MonthlyReports > 0 && <span className="muted small" style={{ marginLeft: 10 }}>{me.limits.MonthlyReports} reports / 30 days on your plan</span>}
          </form>
        </Card>
      )}

      <Card title="Past reports">
        {loading && !data && <p className="muted">Loading…</p>}
        <ErrorBanner message={error} />
        {data && data.length === 0 && <Empty>No reports yet. Research a company above to get started.</Empty>}
        {data && data.length > 0 && (
          <div className="table-wrap"><table>
            <thead><tr><th>Company</th><th>Status</th><th>Created</th><th /></tr></thead>
            <tbody>{data.map((r) => (
              <tr key={r.id}>
                <td><Link href={`/intelligence/${r.id}`}>{r.company_name}</Link>{r.website && <div className="muted small">{r.website}</div>}</td>
                <td><Badge tone={STATUS_TONE[r.status]}>{STATUS_LABEL[r.status]}</Badge></td>
                <td className="small">{fmtDate(r.created_at)}</td>
                <td><Link className="btn btn-sm" href={`/intelligence/${r.id}`}>View</Link></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </Card>
    </>
  );
}
