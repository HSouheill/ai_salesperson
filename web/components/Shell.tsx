"use client";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { createContext, useContext, useEffect, useState } from "react";
import { api } from "@/lib/api";
import type { Me } from "@/lib/types";
import { Badge } from "./ui";

const MeContext = createContext<Me | null>(null);
/** The signed-in organization, its plan limits and status. */
export const useMe = () => useContext(MeContext);

export function applyBranding(color?: string) {
  const root = document.documentElement.style;
  if (!color || !/^#[0-9a-f]{6}$/i.test(color)) {
    ["--accent", "--accent-soft", "--accent-text"].forEach((k) => root.removeProperty(k));
    return;
  }
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(color.slice(i, i + 2), 16));
  const light = (0.299 * r + 0.587 * g + 0.114 * b) / 255 > 0.6; // readable text on the brand colour
  root.setProperty("--accent", color);
  root.setProperty("--accent-soft", `color-mix(in srgb, ${color} 14%, transparent)`);
  root.setProperty("--accent-text", light ? "#111" : "#fff");
}

export default function Shell({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const path = usePathname();
  const [me, setMe] = useState<Me | null>(null);
  const [pending, setPending] = useState(0);

  useEffect(() => {
    // Whether we're logged in lives in an httpOnly cookie this page can't
    // read, so the only way to know is to ask the API. A 401 here is handled
    // by the shared fetch wrapper, which redirects to /login.
    api.me().then((m) => { setMe(m); applyBranding(m.org.branding?.primary_color); }).catch(() => {});
    return () => applyBranding(undefined);
  }, [router]);

  // Keep the approvals badge fresh as the user navigates.
  useEffect(() => {
    if (!me) return;
    api.dashboard().then((s) => setPending(s.awaiting_approval)).catch(() => {});
  }, [me, path]);

  if (!me) return null;
  const brand = me.org.branding;
  const nav = [
    { href: "/", label: "Dashboard" },
    { href: "/agents", label: "Sales agents" },
    { href: "/approvals", label: "Approvals" },
    ...(me.limits.WhiteLabel && !me.is_client && !me.agency_session ? [{ href: "/clients", label: "Clients" }] : []),
    { href: "/settings", label: "Settings" },
    ...(!me.is_client && !me.agency_session ? [{ href: "/billing", label: "Billing" }] : []),
  ];
  const active = (href: string) => (href === "/" ? path === "/" : path.startsWith(href));

  return (
    <MeContext.Provider value={me}>
      <div className="shell">
        <aside className="side">
          <div className="brand">
            {brand?.logo_url && /* eslint-disable-next-line @next/next/no-img-element */ <img src={brand.logo_url} alt="" height={24} style={{ verticalAlign: "middle", marginRight: 8 }} />}
            {brand?.product_name || "AI Salesperson"}
          </div>
          <nav className="nav" aria-label="Main">
            {nav.map((n) => (
              <Link key={n.href} href={n.href} className={active(n.href) ? "active" : ""} aria-current={active(n.href) ? "page" : undefined}>
                {n.label}
                {n.href === "/approvals" && pending > 0 && <Badge tone="warn">{pending}</Badge>}
              </Link>
            ))}
          </nav>
          <div className="side-foot small">
            <div style={{ marginBottom: 8 }}><strong>{me.org.name}</strong><br /><span className="muted">{me.org.plan} plan</span></div>
            <button className="btn btn-sm" onClick={() => { api.logout().finally(() => router.replace("/login")); }}>Log out</button>
          </div>
        </aside>
        <main className="main">
          {me.agency_session && (
            <div className="banner banner-warn" role="status">
              You are viewing <strong>{me.org.name}</strong> as their agency.{" "}
              <button className="btn btn-sm" onClick={() => { api.exitClient().then(() => { window.location.href = "/clients"; }); }}>Back to your agency</button>
            </div>
          )}
          {!me.entitled && (
            <div className="banner banner-error" role="alert">
              {me.is_client ? "Your account is paused. Please contact your provider."
                : me.org.plan === "trial" ? <>Your free trial has ended. <Link href="/billing">Choose a plan</Link> to keep your agents working.</>
                : <>Your subscription is not active. <Link href="/billing">Manage billing</Link> to resume.</>}
            </div>
          )}
          {children}
        </main>
      </div>
    </MeContext.Provider>
  );
}
