"use client";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { api } from "@/lib/api";
import { useAction, useLoad } from "@/lib/hooks";
import { useMe } from "@/components/Shell";
import { Badge, Card, Empty, ErrorBanner, PageHead, money } from "@/components/ui";
import type { PlanName } from "@/lib/types";

const blank = { name: "", slug: "", email: "", password: "", plan: "starter" as PlanName, product_name: "", primary_color: "", logo_url: "" };

export default function Clients() {
  const me = useMe();
  const router = useRouter();
  const { data, error, loading, reload } = useLoad(api.clients);
  const { busy, error: actErr, run } = useAction();
  const [f, setF] = useState(blank);
  const [open, setOpen] = useState(false);
  const set = (k: keyof typeof blank, v: string) => setF((x) => ({ ...x, [k]: v }));

  if (me && !me.limits.WhiteLabel) return <Card title="White-label"><p style={{ margin: 0 }}>Client organizations are part of the Enterprise plan for agencies.</p></Card>;

  async function create() {
    const r = await run("create", () => api.createClient({
      name: f.name, slug: f.slug, email: f.email, password: f.password, plan: f.plan,
      branding: { product_name: f.product_name, primary_color: f.primary_color, logo_url: f.logo_url },
    }));
    if (r) { setF(blank); setOpen(false); void reload(); }
  }
  async function enter(id: string) {
    // clientLogin's response already set the session cookie; nothing to store here.
    const r = await run("enter" + id, () => api.clientLogin(id));
    if (r) window.location.href = "/";
  }
  const loginLink = (slug?: string) => (slug ? `${window.location.origin}/login?org=${slug}` : "");

  return (
    <>
      <PageHead title="Clients" subtitle="Each client gets their own dashboard, agents, campaigns and CRM — under your brand."
        actions={<button className="btn btn-primary" onClick={() => setOpen(!open)}>{open ? "Cancel" : "New client"}</button>} />
      <ErrorBanner message={error || actErr} />
      {open && (
        <Card title="New client">
          <div className="grid grid-2">
            <div className="field"><label htmlFor="cn">Client name</label><input id="cn" value={f.name} onChange={(e) => set("name", e.target.value)} /></div>
            <div className="field"><label htmlFor="cs">Login page handle</label><input id="cs" placeholder="cedar-restaurants" value={f.slug} onChange={(e) => set("slug", e.target.value.toLowerCase())} />
              <div className="hint">Lowercase letters, digits and dashes. Their branded login is /login?org=handle</div></div>
            <div className="field"><label htmlFor="ce">Owner email</label><input id="ce" type="email" value={f.email} onChange={(e) => set("email", e.target.value)} /></div>
            <div className="field"><label htmlFor="cp">Temporary password</label><input id="cp" type="password" autoComplete="new-password" value={f.password} onChange={(e) => set("password", e.target.value)} /></div>
            <div className="field"><label htmlFor="cl">Plan</label><select id="cl" value={f.plan} onChange={(e) => set("plan", e.target.value)}>
              <option value="starter">Starter</option><option value="growth">Growth</option><option value="pro">Pro</option></select></div>
            <div className="field"><label htmlFor="cb">Product name shown to them</label><input id="cb" placeholder="Your Agency Sales AI" value={f.product_name} onChange={(e) => set("product_name", e.target.value)} /></div>
            <div className="field"><label htmlFor="cc">Brand colour</label><input id="cc" placeholder="#3b5bdb" value={f.primary_color} onChange={(e) => set("primary_color", e.target.value)} /></div>
            <div className="field"><label htmlFor="cg">Logo URL (https)</label><input id="cg" value={f.logo_url} onChange={(e) => set("logo_url", e.target.value)} /></div>
          </div>
          <button className="btn btn-primary" disabled={!!busy || !f.name || !f.email || f.password.length < 8} onClick={create}>{busy === "create" ? "Creating…" : "Create client"}</button>
        </Card>
      )}
      {loading && !data && <p className="muted">Loading…</p>}
      {data && data.length === 0 && !open && <Card><Empty>No clients yet. Create your first client to give them a branded workspace.</Empty></Card>}
      <div className="grid grid-2">
        {data?.map(({ org, stats }) => (
          <Card key={org.id} title={org.name} actions={<Badge tone="info">{org.plan}</Badge>}>
            <div className="row small muted" style={{ gap: 16 }}>
              <span>{stats.prospects_identified} prospects</span><span>{stats.qualified_leads} qualified</span>
              <span>{stats.meetings_booked} meetings</span><span>{money(stats.won_revenue)} won</span>
            </div>
            {org.slug && <p className="small" style={{ marginBottom: 0 }}>Login page: <code>{loginLink(org.slug)}</code></p>}
            <div className="row" style={{ marginTop: 12 }}>
              <button className="btn btn-primary btn-sm" disabled={!!busy} onClick={() => enter(org.id)}>Open dashboard</button>
              <select aria-label={`Plan for ${org.name}`} style={{ width: "auto" }} value={org.plan}
                onChange={async (e) => { await run("plan", () => api.updateClient(org.id, { plan: e.target.value as PlanName })); void reload(); }}>
                <option value="starter">Starter</option><option value="growth">Growth</option><option value="pro">Pro</option></select>
            </div>
          </Card>
        ))}
      </div>
      {void router}
    </>
  );
}
