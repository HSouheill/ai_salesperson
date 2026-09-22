import type { ReactNode } from "react";
import type { Stage } from "@/lib/types";

export const STAGE_LABEL: Record<Stage, string> = {
  new: "New", researched: "Researched", contacted: "Contacted", engaged: "Engaged",
  qualified: "Qualified", meeting_booked: "Meeting booked", won: "Won", lost: "Lost", do_not_contact: "Do not contact",
};

export function Badge({ tone = "neutral", children }: { tone?: "neutral" | "good" | "warn" | "bad" | "info"; children: ReactNode }) {
  return <span className={`badge badge-${tone}`}>{children}</span>;
}

const STAGE_TONE: Record<Stage, "neutral" | "good" | "warn" | "bad" | "info"> = {
  new: "neutral", researched: "info", contacted: "info", engaged: "warn",
  qualified: "good", meeting_booked: "good", won: "good", lost: "bad", do_not_contact: "bad",
};
export const StageBadge = ({ stage }: { stage: Stage }) => <Badge tone={STAGE_TONE[stage]}>{STAGE_LABEL[stage]}</Badge>;

export function ScoreBadge({ score, researched }: { score: number; researched: boolean }) {
  if (!researched) return <span className="muted">—</span>;
  const tone = score >= 75 ? "good" : score >= 50 ? "warn" : "neutral";
  return <Badge tone={tone}>{score}/100</Badge>;
}

export function ErrorBanner({ message }: { message: string }) {
  return message ? <div className="banner banner-error" role="alert">{message}</div> : null;
}

export function Card({ title, actions, children }: { title?: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="card">
      {(title || actions) && (
        <header className="card-head">{title && <h2>{title}</h2>}<div className="row">{actions}</div></header>
      )}
      {children}
    </section>
  );
}

export function PageHead({ title, subtitle, actions }: { title: string; subtitle?: string; actions?: ReactNode }) {
  return (
    <div className="page-head">
      <div><h1>{title}</h1>{subtitle && <p className="muted">{subtitle}</p>}</div>
      <div className="row">{actions}</div>
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <p className="empty">{children}</p>;
}

export function List({ items }: { items?: string[] }) {
  if (!items?.length) return <span className="muted">—</span>;
  return <ul className="list">{items.map((x, i) => <li key={i}>{x}</li>)}</ul>;
}

export const fmtDate = (s?: string) => (s ? new Date(s).toLocaleString() : "");

export const money = (n: number) => n.toLocaleString(undefined, { style: "currency", currency: "USD", maximumFractionDigits: 0 });
