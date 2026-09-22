"use client";
import Link from "next/link";
import { api } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import { Badge, Card, Empty, ErrorBanner, PageHead } from "@/components/ui";

const TONE = { draft: "neutral", active: "good", paused: "warn" } as const;

export default function Agents() {
  const { data, error, loading } = useLoad(api.agents);
  return (
    <>
      <PageHead title="Sales agents" subtitle="Each agent has its own strategy, audience and workflow."
        actions={<Link className="btn btn-primary" href="/agents/new">New agent</Link>} />
      <ErrorBanner message={error} />
      {loading && !data && <p className="muted">Loading…</p>}
      {data && data.length === 0 && <Card><Empty>No agents yet. Create your first one — it takes a few minutes.</Empty></Card>}
      <div className="grid grid-2">
        {data?.map((a) => (
          <Link key={a.id} href={`/agents/${a.id}`} className="card" style={{ color: "inherit", textDecoration: "none" }}>
            <div className="card-head"><h2>{a.name}</h2><Badge tone={TONE[a.status]}>{a.status}</Badge></div>
            <p className="muted small" style={{ margin: 0 }}>{a.profile.name} · {a.channel}</p>
            <p className="small" style={{ marginBottom: 0 }}>{a.targeting.industries.join(", ") || "No industries set"}
              {a.targeting.locations.length ? ` — ${a.targeting.locations.join(", ")}` : ""}</p>
            {!a.brain && <p className="small" style={{ marginBottom: 0 }}><Badge tone="warn">Sales brain not generated</Badge></p>}
          </Link>
        ))}
      </div>
    </>
  );
}
