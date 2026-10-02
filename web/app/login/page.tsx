"use client";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { errMsg } from "@/lib/hooks";
import { applyBranding } from "@/components/Shell";
import { ErrorBanner } from "@/components/ui";
import { Turnstile, captchaRequired } from "@/components/Turnstile";
import type { Branding } from "@/lib/types";

export default function LoginPage() {
  const router = useRouter();
  const [mode, setMode] = useState<"login" | "signup">("login");
  const [org, setOrg] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [brand, setBrand] = useState<Branding>({});
  const [branded, setBranded] = useState(false);
  const [captchaToken, setCaptchaToken] = useState<string | null>(null);

  // An agency's client opens /login?org=handle and sees the agency's brand, not ours.
  useEffect(() => {
    const slug = new URLSearchParams(window.location.search).get("org");
    if (!slug) return;
    setBranded(true);
    setMode("login");
    api.publicBranding(slug).then((b) => { setBrand(b); applyBranding(b.primary_color); }).catch(() => {});
    return () => applyBranding(undefined);
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true); setError("");
    try {
      // The response also carries a bearer token (for API/CLI use), but the
      // dashboard ignores it: the server already set the session as an
      // httpOnly cookie, which the browser will send on its own from here.
      if (mode === "signup") await api.signup(org, email, password, captchaToken ?? undefined);
      else await api.login(email, password);
      router.replace("/");
    } catch (err) { setError(errMsg(err)); }
    finally { setBusy(false); }
  }

  const blockedByCaptcha = mode === "signup" && captchaRequired && !captchaToken;

  return (
    <div className="auth">
      <form className="card" onSubmit={submit}>
        {brand.logo_url && /* eslint-disable-next-line @next/next/no-img-element */ <img src={brand.logo_url} alt="" height={36} style={{ marginBottom: 8 }} />}
        <h1 style={{ marginBottom: 4 }}>{brand.product_name || "AI Salesperson"}</h1>
        <p className="muted" style={{ marginTop: 0 }}>{branded ? "Sign in to your workspace." : "Your AI sales workforce. 14-day free trial."}</p>
        {!branded && (
          <div className="tabs" role="tablist">
            <button type="button" role="tab" aria-selected={mode === "login"} className={mode === "login" ? "on" : ""} onClick={() => setMode("login")}>Log in</button>
            <button type="button" role="tab" aria-selected={mode === "signup"} className={mode === "signup" ? "on" : ""} onClick={() => setMode("signup")}>Create account</button>
          </div>
        )}
        <ErrorBanner message={error} />
        {mode === "signup" && (
          <div className="field"><label htmlFor="org">Company name</label>
            <input id="org" value={org} onChange={(e) => setOrg(e.target.value)} required autoComplete="organization" /></div>
        )}
        <div className="field"><label htmlFor="email">Email</label>
          <input id="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required autoComplete="email" /></div>
        <div className="field"><label htmlFor="pw">Password</label>
          <input id="pw" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required minLength={8} maxLength={72}
            autoComplete={mode === "signup" ? "new-password" : "current-password"} />
          {mode === "signup" && <div className="hint">At least 8 characters.</div>}</div>
        {mode === "signup" && <Turnstile onToken={setCaptchaToken} />}
        <button className="btn btn-primary" style={{ width: "100%" }} disabled={busy || blockedByCaptcha}>
          {busy ? "Please wait…" : mode === "signup" ? "Start free trial" : "Log in"}
        </button>
      </form>
    </div>
  );
}
