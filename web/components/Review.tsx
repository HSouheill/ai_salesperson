"use client";
import { useState } from "react";
import { api } from "@/lib/api";
import { useAction } from "@/lib/hooks";
import type { Message } from "@/lib/types";
import { ErrorBanner } from "./ui";

/** A drafted message awaiting human approval (or a failed one to retry): edit, send, or reject. */
export default function PendingMessage({ m, onDone }: { m: Message; onDone: () => void }) {
  const [body, setBody] = useState(m.body);
  const { busy, error, run } = useAction();

  const review = async (approve: boolean) => {
    const r = await run(approve ? "approve" : "reject", () => api.review(m.id, approve, approve ? body : undefined));
    if (r) onDone();
  };

  return (
    <div className="stack">
      <ErrorBanner message={error} />
      {m.status === "failed" && (
        <div className="banner banner-error" role="alert" style={{ marginBottom: 0 }}>
          Couldn’t send: {m.error || "unknown error"}. Check your email connection in Settings, then retry.
        </div>
      )}
      {m.subject && <div className="small"><span className="muted">Subject:</span> {m.subject}</div>}
      <label htmlFor={`b-${m.id}`} className="muted small">Message ({m.channel}) — edit before sending if you like</label>
      <textarea id={`b-${m.id}`} value={body} onChange={(e) => setBody(e.target.value)} />
      <div className="row">
        <button className="btn btn-primary btn-sm" disabled={!!busy || !body.trim()} onClick={() => review(true)}>
          {busy === "approve" ? "Sending…" : m.status === "failed" ? "Retry sending" : "Approve & send"}
        </button>
        <button className="btn btn-danger btn-sm" disabled={!!busy} onClick={() => review(false)}>Reject</button>
      </div>
    </div>
  );
}
