"use client";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { api } from "@/lib/api";
import { useAction } from "@/lib/hooks";
import { Badge, Card, ErrorBanner, PageHead } from "@/components/ui";

const split = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);
const STEPS = ["Your business", "Who to sell to", "Launch"];

export default function NewAgent() {
  const router = useRouter();
  const { busy, error, setError, run } = useAction();
  const [step, setStep] = useState(0);
  const [website, setWebsite] = useState("");
  const [extra, setExtra] = useState<{ target_market?: string; faqs?: { question: string; answer: string }[] }>({});
  const [f, setF] = useState({
    agentName: "", bizName: "", description: "", products: "", services: "", valueProp: "",
    industries: "", locations: "", sizes: "", goals: "", persona: "Friendly, concise and professional.",
    channel: "email", approval: true,
  });
  const set = (k: keyof typeof f, v: string | boolean) => setF((p) => ({ ...p, [k]: v }));

  async function analyze() {
    const p = await run("analyze", () => api.analyzeWebsite(website));
    if (!p) return;
    setExtra({ target_market: p.target_market, faqs: p.faqs }); // kept so replies can use the company's own FAQs
    setF((x) => ({
      ...x, bizName: p.name || x.bizName, description: p.description || x.description,
      products: (p.products ?? []).join(", ") || x.products, services: (p.services ?? []).join(", ") || x.services,
      valueProp: p.value_proposition || x.valueProp, agentName: x.agentName || (p.name ? `${p.name} Sales Agent` : ""),
    }));
    setStep(0);
  }

  async function create() {
    const agent = await run("create", () => api.createAgent({
      name: f.agentName, persona: f.persona, channel: f.channel, require_approval: f.approval,
      profile: { website, name: f.bizName, description: f.description, products: split(f.products), services: split(f.services), value_proposition: f.valueProp, target_market: extra.target_market, faqs: extra.faqs },
      targeting: { industries: split(f.industries), locations: split(f.locations), company_sizes: split(f.sizes), goals: f.goals },
    }));
    if (!agent) return;
    // Generating the brain is a separate step so a model failure doesn't lose the agent.
    await run("brain", () => api.generateBrain(agent.id));
    router.replace(`/agents/${agent.id}`);
  }

  const ok0 = f.bizName.trim() && f.description.trim() && f.agentName.trim();
  const ok1 = split(f.industries).length > 0;

  return (
    <>
      <PageHead title="New sales agent" subtitle="Describe your business and who you want to sell to. We'll build the strategy." />
      <div className="steps">{STEPS.map((s, i) => <Badge key={s} tone="info">{i === step ? `● ${s}` : s}</Badge>)}</div>
      <ErrorBanner message={error} />

      {step === 0 && (
        <Card title="Your business">
          <div className="field">
            <label htmlFor="web">Website (optional)</label>
            <div className="row" style={{ flexWrap: "nowrap" }}>
              <input id="web" placeholder="https://yourcompany.com" value={website} onChange={(e) => setWebsite(e.target.value)} />
              <button className="btn" disabled={!website.trim() || !!busy} onClick={analyze}>{busy === "analyze" ? "Reading…" : "Auto-fill"}</button>
            </div>
            <div className="hint">We read only public pages that allow automated access, then you can edit everything below.</div>
          </div>
          <div className="field"><label htmlFor="an">Agent name</label><input id="an" placeholder="Restaurant Hunter" value={f.agentName} onChange={(e) => set("agentName", e.target.value)} /></div>
          <div className="field"><label htmlFor="bn">Company name</label><input id="bn" value={f.bizName} onChange={(e) => set("bizName", e.target.value)} /></div>
          <div className="field"><label htmlFor="d">What do you sell?</label><textarea id="d" value={f.description} onChange={(e) => set("description", e.target.value)} /></div>
          <div className="field"><label htmlFor="p">Products (comma separated)</label><input id="p" value={f.products} onChange={(e) => set("products", e.target.value)} /></div>
          <div className="field"><label htmlFor="s">Services (comma separated)</label><input id="s" value={f.services} onChange={(e) => set("services", e.target.value)} /></div>
          <div className="field"><label htmlFor="v">Value proposition</label><input id="v" value={f.valueProp} onChange={(e) => set("valueProp", e.target.value)} /></div>
          <button className="btn btn-primary" disabled={!ok0} onClick={() => { setError(""); setStep(1); }}>Next</button>
        </Card>
      )}

      {step === 1 && (
        <Card title="Who do you want to sell to?">
          <div className="field"><label htmlFor="i">Industries (comma separated)</label><input id="i" placeholder="Restaurants, Cafes" value={f.industries} onChange={(e) => set("industries", e.target.value)} /></div>
          <div className="field"><label htmlFor="l">Locations (comma separated)</label><input id="l" placeholder="Lebanon, UAE" value={f.locations} onChange={(e) => set("locations", e.target.value)} /></div>
          <div className="field"><label htmlFor="z">Company sizes (comma separated)</label><input id="z" placeholder="1-10, 11-50" value={f.sizes} onChange={(e) => set("sizes", e.target.value)} /></div>
          <div className="field"><label htmlFor="g">Sales goals</label><input id="g" placeholder="10 demo calls per month" value={f.goals} onChange={(e) => set("goals", e.target.value)} /></div>
          <div className="row"><button className="btn" onClick={() => setStep(0)}>Back</button>
            <button className="btn btn-primary" disabled={!ok1} onClick={() => setStep(2)}>Next</button></div>
        </Card>
      )}

      {step === 2 && (
        <Card title="Launch settings">
          <div className="field"><label htmlFor="pe">Personality</label><input id="pe" value={f.persona} onChange={(e) => set("persona", e.target.value)} /></div>
          <div className="field"><label htmlFor="c">Channel</label>
            <select id="c" value={f.channel} onChange={(e) => set("channel", e.target.value)}><option value="email">Email</option><option value="whatsapp">WhatsApp</option></select></div>
          <div className="field"><label className="row" style={{ fontWeight: 400 }}>
            <input type="checkbox" style={{ width: "auto" }} checked={f.approval} onChange={(e) => set("approval", e.target.checked)} />
            Require my approval before any message is sent (recommended)</label></div>
          <div className="row"><button className="btn" onClick={() => setStep(1)}>Back</button>
            <button className="btn btn-primary" disabled={!!busy} onClick={create}>
              {busy === "create" ? "Creating…" : busy === "brain" ? "Building your sales strategy…" : "Create agent & build strategy"}</button></div>
        </Card>
      )}
    </>
  );
}
