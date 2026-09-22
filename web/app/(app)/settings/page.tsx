"use client";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { useAction, useLoad } from "@/lib/hooks";
import { useMe } from "@/components/Shell";
import { Badge, Card, ErrorBanner, PageHead, fmtDate } from "@/components/ui";
import type { CheckResult, EmailSettings, OrgSettings, SettingsResponse, WhatsAppSettings } from "@/lib/types";

const emptyEmail: EmailSettings = { from_name: "", from_address: "", smtp: { host: "", port: 587, username: "", password: "", security: "starttls" } };
const emptyIMAP = { host: "", port: 993, username: "", password: "", security: "tls" };
const emptyWA: WhatsAppSettings = { phone_number_id: "", access_token: "", app_secret: "", verify_token: "", template_name: "", template_lang: "en" };

function Field({ id, label, hint, children }: { id: string; label: string; hint?: string; children: React.ReactNode }) {
  return <div className="field"><label htmlFor={id}>{label}</label>{children}{hint && <div className="hint">{hint}</div>}</div>;
}

function Secret({ id, label, isSet, value, onChange, hint }: { id: string; label: string; isSet?: boolean; value: string; onChange: (v: string) => void; hint?: string }) {
  return (
    <Field id={id} label={label} hint={hint}>
      <input id={id} type="password" autoComplete="new-password" value={value} onChange={(e) => onChange(e.target.value)}
        placeholder={isSet ? "•••••••• saved — leave blank to keep" : ""} />
    </Field>
  );
}

function Copy({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <div className="row" style={{ flexWrap: "nowrap" }}>
      <input readOnly value={text} onFocus={(e) => e.currentTarget.select()} aria-label="URL" />
      <button className="btn btn-sm" onClick={async () => { try { await navigator.clipboard.writeText(text); setDone(true); setTimeout(() => setDone(false), 1500); } catch { /* clipboard blocked */ } }}>
        {done ? "Copied" : "Copy"}
      </button>
    </div>
  );
}

export default function SettingsPage() {
  const me = useMe();
  const { data, error, loading, setData } = useLoad(api.settings);
  const { busy, error: actErr, run } = useAction();
  const [email, setEmail] = useState<EmailSettings | null>(null);
  const [useIMAP, setUseIMAP] = useState(false);
  const [wa, setWa] = useState<WhatsAppSettings | null>(null);
  const [rest, setRest] = useState({ sender: "", notify: "", webhook: "", webhookSecret: "", hubspot: "", places: "" });
  const [saved, setSaved] = useState("");
  const [tests, setTests] = useState<{ smtp: CheckResult; imap: CheckResult } | null>(null);

  function load(d: SettingsResponse) {
    const s = d.settings;
    setEmail(s.email ? { ...s.email, smtp: { ...emptyEmail.smtp, ...s.email.smtp } } : null);
    setUseIMAP(!!s.email?.imap);
    setWa(s.whatsapp ? { ...emptyWA, ...s.whatsapp } : null);
    setRest({ sender: s.sender_address ?? "", notify: (s.notify_emails ?? []).join(", "), webhook: s.integrations.webhook_url ?? "",
      webhookSecret: "", hubspot: "", places: "" });
  }
  useEffect(() => { if (data) load(data); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [data === null]);

  if (loading && !data) return <p className="muted">Loading…</p>;
  if (!data) return <ErrorBanner message={error} />;
  const set = data.secrets_set;

  async function save() {
    const body: OrgSettings = {
      email: email ? { ...email, smtp: { ...email.smtp, port: Number(email.smtp.port) || 0 },
        imap: useIMAP ? { ...(email.imap ?? emptyIMAP), port: Number(email.imap?.port ?? 993) || 0 } : null } : null,
      whatsapp: wa,
      sender_address: rest.sender,
      notify_emails: rest.notify.split(",").map((x) => x.trim()).filter(Boolean),
      google_places_key: rest.places,
      integrations: { webhook_url: rest.webhook, webhook_secret: rest.webhookSecret, hubspot_token: rest.hubspot },
    };
    const r = await run("save", () => api.saveSettings(body));
    if (r) { setData(r); load(r); setSaved("Settings saved."); setTests(null); } else setSaved("");
  }

  const upSMTP = (k: string, v: string | number) => setEmail((e) => e && { ...e, smtp: { ...e.smtp, [k]: v } });
  const upIMAP = (k: string, v: string | number) => setEmail((e) => e && { ...e, imap: { ...(e.imap ?? emptyIMAP), [k]: v } });

  return (
    <>
      <PageHead title="Settings" subtitle="Connect your own accounts. Messages are sent from your mailbox and numbers, never from ours."
        actions={<button className="btn btn-primary" disabled={!!busy} onClick={save}>{busy === "save" ? "Saving…" : "Save settings"}</button>} />
      <ErrorBanner message={actErr} />
      {saved && <div className="banner banner-good" role="status">{saved}</div>}

      <Card title="Email" actions={email ? <button className="btn btn-sm btn-danger" onClick={() => setEmail(null)}>Disconnect</button>
        : <button className="btn btn-sm" onClick={() => setEmail(emptyEmail)}>Connect email</button>}>
        {!email ? <p className="muted" style={{ margin: 0 }}>Not connected. Add your SMTP details so agents can send from your address, and IMAP so replies are picked up automatically.</p> : (
          <>
            <div className="grid grid-2">
              <Field id="fn" label="From name"><input id="fn" value={email.from_name} onChange={(e) => setEmail({ ...email, from_name: e.target.value })} /></Field>
              <Field id="fa" label="From address"><input id="fa" type="email" value={email.from_address} onChange={(e) => setEmail({ ...email, from_address: e.target.value })} /></Field>
            </div>
            <h3>Sending (SMTP)</h3>
            <div className="grid grid-2">
              <Field id="sh" label="Server"><input id="sh" placeholder="smtp.yourprovider.com" value={email.smtp.host} onChange={(e) => upSMTP("host", e.target.value)} /></Field>
              <Field id="sp" label="Port"><input id="sp" type="number" value={email.smtp.port} onChange={(e) => upSMTP("port", e.target.value)} /></Field>
              <Field id="su" label="Username"><input id="su" autoComplete="off" value={email.smtp.username} onChange={(e) => upSMTP("username", e.target.value)} /></Field>
              <Secret id="spw" label="Password" isSet={set.smtp_password} value={email.smtp.password ?? ""} onChange={(v) => upSMTP("password", v)} />
              <Field id="ss" label="Security"><select id="ss" value={email.smtp.security} onChange={(e) => upSMTP("security", e.target.value)}>
                <option value="starttls">STARTTLS (port 587)</option><option value="tls">TLS (port 465)</option></select></Field>
            </div>
            <h3>Receiving replies (IMAP)</h3>
            <label className="row" style={{ fontWeight: 400 }}>
              <input type="checkbox" style={{ width: "auto" }} checked={useIMAP} onChange={(e) => setUseIMAP(e.target.checked)} />
              Read replies from my mailbox automatically
            </label>
            <p className="hint">Only new mail from people your agents have contacted is used. Your other mail is never touched, marked as read or moved.</p>
            {useIMAP && (
              <div className="grid grid-2">
                <Field id="ih" label="Server"><input id="ih" placeholder="imap.yourprovider.com" value={email.imap?.host ?? ""} onChange={(e) => upIMAP("host", e.target.value)} /></Field>
                <Field id="ip" label="Port"><input id="ip" type="number" value={email.imap?.port ?? 993} onChange={(e) => upIMAP("port", e.target.value)} /></Field>
                <Field id="iu" label="Username"><input id="iu" autoComplete="off" value={email.imap?.username ?? ""} onChange={(e) => upIMAP("username", e.target.value)} /></Field>
                <Secret id="ipw" label="Password" isSet={set.imap_password} value={email.imap?.password ?? ""} onChange={(v) => upIMAP("password", v)} />
                <Field id="is" label="Security"><select id="is" value={email.imap?.security ?? "tls"} onChange={(e) => upIMAP("security", e.target.value)}>
                  <option value="tls">TLS (port 993)</option><option value="starttls">STARTTLS (port 143)</option></select></Field>
                <Field id="if" label="Folder"><input id="if" placeholder="INBOX" value={email.imap?.folder ?? ""} onChange={(e) => upIMAP("folder", e.target.value)} /></Field>
              </div>
            )}
            <div className="row">
              <button className="btn" disabled={!!busy} onClick={async () => { const t = await run("test", () => api.testSettings()); if (t) setTests(t); }}>
                {busy === "test" ? "Testing…" : "Test saved connection"}</button>
              <span className="muted small">Save first — the test uses the saved settings and sends nothing.</span>
            </div>
            {tests && (["smtp", "imap"] as const).map((k) => tests[k].configured && (
              <div key={k} className={`banner ${tests[k].ok ? "banner-good" : "banner-error"}`} style={{ marginTop: 10 }}>
                {k === "smtp" ? "Sending" : "Receiving"}: {tests[k].ok ? "connected" : tests[k].error}
              </div>))}
          </>
        )}
      </Card>

      <Card title="WhatsApp" actions={wa ? <button className="btn btn-sm btn-danger" onClick={() => setWa(null)}>Disconnect</button>
        : <button className="btn btn-sm" onClick={() => setWa(emptyWA)}>Connect WhatsApp</button>}>
        {!wa ? <p className="muted" style={{ margin: 0 }}>Uses your own WhatsApp Business Cloud API number. Only message people who agreed to be contacted on WhatsApp.</p> : (
          <>
            <div className="grid grid-2">
              <Field id="wp" label="Phone number ID" hint="From Meta's WhatsApp Manager"><input id="wp" value={wa.phone_number_id} onChange={(e) => setWa({ ...wa, phone_number_id: e.target.value })} /></Field>
              <Secret id="wt" label="Access token" isSet={set.whatsapp_access_token} value={wa.access_token ?? ""} onChange={(v) => setWa({ ...wa, access_token: v })} />
              <Field id="wn" label="First-contact template name" hint="WhatsApp only allows business-started chats through an approved template with one text variable ({{1}}).">
                <input id="wn" value={wa.template_name ?? ""} onChange={(e) => setWa({ ...wa, template_name: e.target.value })} /></Field>
              <Field id="wl" label="Template language"><input id="wl" value={wa.template_lang ?? ""} onChange={(e) => setWa({ ...wa, template_lang: e.target.value })} /></Field>
              <Secret id="wa" label="App secret" isSet={set.whatsapp_app_secret} value={wa.app_secret ?? ""} onChange={(v) => setWa({ ...wa, app_secret: v })} hint="Used to verify that incoming messages really come from Meta." />
              <Secret id="wv" label="Webhook verify token" isSet={set.whatsapp_verify_token} value={wa.verify_token ?? ""} onChange={(v) => setWa({ ...wa, verify_token: v })} hint="Any secret text; enter the same value in Meta's webhook setup." />
            </div>
            <Field id="wu" label="Webhook URL to give Meta"><Copy text={data.inbound_urls.whatsapp} /></Field>
          </>
        )}
      </Card>

      <Card title="Replies from other email tools">
        <p className="muted small" style={{ marginTop: 0 }}>If you can’t use IMAP, have your email provider (or an automation tool) POST replies here as JSON <code>{"{ from, subject, text }"}</code> or a standard inbound-email form post.</p>
        <Copy text={data.inbound_urls.email} />
        <div style={{ marginTop: 10 }}>
          <button className="btn btn-sm" disabled={!!busy} onClick={async () => { if (confirm("Generate new webhook URLs? The old ones stop working immediately.")) { const r = await run("rotate", () => api.rotateInboundToken()); if (r) setData(r); } }}>
            Generate new URLs</button>
        </div>
      </Card>

      <Card title="Your team & sender details">
        <Field id="nt" label="Alert these people when a lead qualifies (comma separated)"><input id="nt" value={rest.notify} onChange={(e) => setRest({ ...rest, notify: e.target.value })} placeholder="sales@yourcompany.com" /></Field>
        <Field id="sa" label="Postal address for the email footer" hint="Many countries require a sender address in commercial email. Every outreach email also carries a one-line opt-out."><input id="sa" value={rest.sender} onChange={(e) => setRest({ ...rest, sender: e.target.value })} /></Field>
      </Card>

      <Card title="CRM integrations" actions={!data.integrations_allowed && <Badge tone="warn">Pro plan</Badge>}>
        <fieldset disabled={!data.integrations_allowed} style={{ border: 0, padding: 0, margin: 0 }}>
          <p className="muted small" style={{ marginTop: 0 }}>Qualified leads, booked meetings and won deals are pushed to your CRM automatically.</p>
          <div className="grid grid-2">
            <Field id="cw" label="Webhook URL (https)" hint="Works with Zapier, Make, or your own system. Payloads are signed with X-AISP-Signature."><input id="cw" value={rest.webhook} onChange={(e) => setRest({ ...rest, webhook: e.target.value })} /></Field>
            <Secret id="cs" label="Webhook signing secret" isSet={set.webhook_secret} value={rest.webhookSecret} onChange={(v) => setRest({ ...rest, webhookSecret: v })} />
            <Secret id="ch" label="HubSpot private-app token" isSet={set.hubspot_token} value={rest.hubspot} onChange={(v) => setRest({ ...rest, hubspot: v })} hint="Creates contacts and moves them to Customer when you win the deal." />
          </div>
        </fieldset>
      </Card>

      <Card title="Prospect discovery">
        <p className="muted small" style={{ marginTop: 0 }}>Discovery from OpenStreetMap works out of the box. To also use Google Maps listings, add your own Google Places API key (your account, your quota and terms).</p>
        <Secret id="gp" label="Google Places API key" isSet={set.google_places_key} value={rest.places} onChange={(v) => setRest({ ...rest, places: v })} />
      </Card>

      {me?.limits.APIAccess ? <ApiKeys /> : (
        <Card title="API access"><p className="muted" style={{ margin: 0 }}>API keys are available on the Pro plan.</p></Card>
      )}
    </>
  );
}

function ApiKeys() {
  const { data, error, reload } = useLoad(api.apiKeys);
  const { busy, error: actErr, run } = useAction();
  const [name, setName] = useState("");
  const [fresh, setFresh] = useState("");
  return (
    <Card title="API keys">
      <ErrorBanner message={error || actErr} />
      {fresh && (
        <div className="banner banner-good">Copy your new key now — it won’t be shown again.<div style={{ marginTop: 8 }}><Copy text={fresh} /></div></div>
      )}
      <p className="muted small" style={{ marginTop: 0 }}>Send <code>Authorization: Bearer &lt;key&gt;</code> to the same API the dashboard uses.</p>
      {data?.length ? (
        <div className="table-wrap"><table><thead><tr><th>Name</th><th>Key</th><th>Created</th><th /></tr></thead><tbody>
          {data.map((k) => (<tr key={k.id}><td>{k.name}</td><td><code>{k.prefix}…</code></td><td className="small">{fmtDate(k.created_at)}</td>
            <td><button className="btn btn-sm btn-danger" onClick={async () => { if (confirm(`Revoke “${k.name}”?`)) { await run("del", () => api.deleteAPIKey(k.id)); void reload(); } }}>Revoke</button></td></tr>))}
        </tbody></table></div>
      ) : <p className="muted small">No keys yet.</p>}
      <div className="row" style={{ marginTop: 10, flexWrap: "nowrap" }}>
        <input aria-label="Key name" placeholder="Key name, e.g. Zapier" value={name} onChange={(e) => setName(e.target.value)} />
        <button className="btn btn-primary" disabled={!!busy || !name.trim()} onClick={async () => {
          const r = await run("add", () => api.createAPIKey(name));
          if (r) { setFresh(r.key); setName(""); void reload(); }
        }}>Create key</button>
      </div>
    </Card>
  );
}
