"use client";
import Link from "next/link";
import { api } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import { useMe } from "@/components/Shell";
import { Card, Empty, ErrorBanner, PageHead, STAGE_LABEL, money } from "@/components/ui";
import type { Stage } from "@/lib/types";

const FUNNEL: Stage[] = ["new", "researched", "contacted", "engaged", "qualified", "meeting_booked", "won"];

function trialDaysLeft(end?: string) {
  if (!end) return 0;
  return Math.max(0, Math.ceil((new Date(end).getTime() - Date.now()) / 86400000));
}

export default function Dashboard() {
  const me = useMe();
  const { data: s, error, loading } = useLoad(api.dashboard);
  const settings = useLoad(api.settings);
  if (loading && !s) return <p className="muted">Loading…</p>;
  if (!s) return <ErrorBanner message={error} />;

  const tiles: [string, string][] = [
    [s.prospects_identified.toLocaleString(), "Prospects identified"],
    [s.prospects_researched.toLocaleString(), "Prospects researched"],
    [s.outreach_prepared.toLocaleString(), "Outreach messages prepared"],
    [s.active_conversations.toLocaleString(), "Active conversations"],
    [s.qualified_leads.toLocaleString(), "Qualified leads"],
    [s.meetings_booked.toLocaleString(), "Meetings booked"],
    [money(s.estimated_pipeline), "Estimated pipeline"],
    [money(s.won_revenue), `Won revenue (${s.won} deal${s.won === 1 ? "" : "s"})`],
  ];
  const max = Math.max(1, ...FUNNEL.map((k) => s.by_stage[k] ?? 0));
  const ch = settings.data?.channels;
  const noChannel = ch && !ch.email && !ch.whatsapp;
  const days = me?.org.plan === "trial" && me.entitled ? trialDaysLeft(me.org.trial_ends_at) : null;

  return (
    <>
      <PageHead title="Dashboard" subtitle="What your AI sales operation is doing right now." />
      {days !== null && !me?.is_client && (
        <div className="banner banner-warn">Free trial: {days} day{days === 1 ? "" : "s"} left. <Link href="/billing">Choose a plan →</Link></div>
      )}
      {noChannel && (
        <div className="banner banner-warn">
          Connect your email (or WhatsApp) so your agents can send and receive messages. <Link href="/settings">Open Settings →</Link>
        </div>
      )}
      {s.awaiting_approval > 0 && (
        <div className="banner banner-warn">
          {s.awaiting_approval} message{s.awaiting_approval > 1 ? "s are" : " is"} waiting for your approval. <Link href="/approvals">Review now →</Link>
        </div>
      )}
      <div className="grid grid-3" style={{ marginBottom: 16 }}>
        {tiles.map(([n, l]) => (<div className="stat" key={l}><div className="n">{n}</div><div className="l">{l}</div></div>))}
      </div>
      <Card title="Pipeline">
        {s.prospects_identified === 0 ? (
          <Empty>No prospects yet. <Link href="/agents">Open a sales agent</Link> to import a list or discover businesses.</Empty>
        ) : (
          <div className="funnel">
            {FUNNEL.map((k) => (
              <div className="funnel-row" key={k}>
                <span>{STAGE_LABEL[k]}</span>
                <div className="bar" role="img" aria-label={`${STAGE_LABEL[k]}: ${s.by_stage[k] ?? 0}`}><div style={{ width: `${((s.by_stage[k] ?? 0) / max) * 100}%` }} /></div>
                <span className="muted">{s.by_stage[k] ?? 0}</span>
              </div>
            ))}
            {((s.by_stage.lost ?? 0) + (s.by_stage.do_not_contact ?? 0)) > 0 && (
              <p className="muted small" style={{ margin: "8px 0 0" }}>
                {s.by_stage.lost ?? 0} lost · {s.by_stage.do_not_contact ?? 0} opted out
              </p>
            )}
          </div>
        )}
      </Card>
    </>
  );
}
