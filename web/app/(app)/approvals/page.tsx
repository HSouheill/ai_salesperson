"use client";
import Link from "next/link";
import { api } from "@/lib/api";
import { useLoad } from "@/lib/hooks";
import PendingMessage from "@/components/Review";
import { Card, Empty, ErrorBanner, PageHead, fmtDate } from "@/components/ui";
import type { Prospect } from "@/lib/types";

export default function Approvals() {
  const { data, error, loading, reload } = useLoad(async () => {
    const [pendingMsgs, failedMsgs, agents] = await Promise.all([api.messages("pending_approval"), api.messages("failed"), api.agents()]);
    const messages = [...failedMsgs, ...pendingMsgs]; // failed sends first: they need attention
    const lists = await Promise.all(agents.map((a) => api.prospects(a.id)));
    const names = new Map<string, Prospect>();
    lists.flat().forEach((p) => names.set(p.id, p));
    return { messages, names };
  });

  return (
    <>
      <PageHead title="Approvals" subtitle="Nothing is sent until you approve it." />
      <ErrorBanner message={error} />
      {loading && !data && <p className="muted">Loading…</p>}
      {data && data.messages.length === 0 && <Card><Empty>You're all caught up — no messages are waiting.</Empty></Card>}
      {data?.messages.map((m) => {
        const p = data.names.get(m.prospect_id);
        return (
          <Card key={m.id} title={p ? p.name : "Prospect"} actions={<span className="muted small">{m.kind} · {fmtDate(m.created_at)}</span>}>
            {p && <p className="muted small" style={{ marginTop: 0 }}>
              {[p.contact_name, p.contact.email ?? p.contact.phone].filter(Boolean).join(" · ")} · <Link href={`/prospects/${p.id}`}>View conversation</Link>
            </p>}
            <PendingMessage m={m} onDone={reload} />
          </Card>
        );
      })}
    </>
  );
}
