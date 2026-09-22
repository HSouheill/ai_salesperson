"use client";
import { useCallback, useEffect, useRef, useState } from "react";

export function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : "Something went wrong";
}

/** Loads data on mount / when deps change and exposes reload(). */
export function useLoad<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const fnRef = useRef(fn);
  fnRef.current = fn;

  const reload = useCallback(async () => {
    setLoading(true);
    try { setData(await fnRef.current()); setError(""); }
    catch (e) { setError(errMsg(e)); }
    finally { setLoading(false); }
  }, []);

  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { void reload(); }, deps);
  return { data, error, loading, reload, setData };
}

/** Runs an async action, tracking busy/error state for buttons. */
export function useAction() {
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const run = useCallback(async <T,>(key: string, fn: () => Promise<T>): Promise<T | undefined> => {
    setBusy(key); setError("");
    try { return await fn(); }
    catch (e) { setError(errMsg(e)); }
    finally { setBusy(""); }
  }, []);
  return { busy, error, setError, run };
}
