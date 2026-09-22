"use client";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { useAction, useLoad } from "@/lib/hooks";
import { useMe } from "@/components/Shell";
import { Badge, Card, ErrorBanner, PageHead, money } from "@/components/ui";
import type { PlanName } from "@/lib/types";

const ORDER: PlanName[] = ["starter", "growth", "pro", "enterprise"];

export default function Billing() {
  const me = useMe();
  const plans = useLoad(api.plans);
  const { busy, error, run } = useAction();
  const [status, setStatus] = useState("");
  useEffect(() => { setStatus(new URLSearchParams(window.location.search).get("status") ?? ""); }, []);

  if (!me) return null;
  const cur = me.org.plan;
  const go = async (p: PlanName) => { const r = await run(p, () => api.checkout(p)); if (r) window.location.href = r.url; };

  return (
    <>
      <PageHead title="Billing" subtitle={`You are on the ${cur} plan${me.org.billing?.status ? ` (${me.org.billing.status})` : ""}.`}
        actions={me.org.billing?.customer_id && me.billing_available ? (
          <button className="btn" disabled={!!busy} onClick={async () => { const r = await run("portal", () => api.portal()); if (r) window.location.href = r.url; }}>
            {busy === "portal" ? "Opening…" : "Manage subscription"}</button>) : undefined} />
      {status === "success" && <div className="banner banner-good" role="status">Thanks! Your plan will update in a moment — refresh if it hasn’t.</div>}
      {status === "cancelled" && <div className="banner banner-warn" role="status">Checkout was cancelled. Nothing was charged.</div>}
      {!me.billing_available && <div className="banner banner-warn">Online payments aren’t enabled on this server yet.</div>}
      <ErrorBanner message={error || plans.error} />
      <div className="grid grid-2">
        {plans.data?.sort((a, b) => ORDER.indexOf(a.plan) - ORDER.indexOf(b.plan)).map((p) => (
          <Card key={p.plan} title={p.name} actions={p.plan === cur ? <Badge tone="good">Current plan</Badge> : undefined}>
            <div style={{ fontSize: 28, fontWeight: 700 }}>{p.price_usd_month ? <>{money(p.price_usd_month)}<span className="muted small"> / month</span></> : "Custom"}</div>
            <ul className="list" style={{ margin: "12px 0" }}>{p.highlights.map((h) => <li key={h}>{h}</li>)}</ul>
            <p className="muted small">
              {p.limits.MaxAgents} agent{p.limits.MaxAgents > 1 ? "s" : ""} · up to {p.limits.MaxProspects.toLocaleString()} prospects · {p.limits.DailySends.toLocaleString()} messages/day per agent
            </p>
            {p.plan === "enterprise"
              ? <a className="btn" href="mailto:sales@example.com?subject=Enterprise%20plan">Contact sales</a>
              : <button className="btn btn-primary" disabled={!!busy || p.plan === cur || !me.billing_available} onClick={() => go(p.plan)}>
                  {busy === p.plan ? "Redirecting…" : p.plan === cur ? "Current plan" : cur === "trial" ? "Choose plan" : "Switch to this plan"}</button>}
          </Card>
        ))}
      </div>
    </>
  );
}
