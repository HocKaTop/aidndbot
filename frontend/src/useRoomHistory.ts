import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";
import type { HistoryEvent, HistoryPage } from "./types";

type View = {
  roomId?: string;
  events: HistoryEvent[];
  nextCursor: string | null;
  initialized: boolean;
  loading: boolean;
  loadingOlder: boolean;
  error: string;
  olderError: string;
};
type Context = { view: View; refresh?: Promise<void>; again: boolean };
const empty = (roomId?: string): View => ({
  roomId,
  events: [],
  nextCursor: null,
  initialized: false,
  loading: false,
  loadingOlder: false,
  error: "",
  olderError: "",
});

function merge(existing: HistoryEvent[], incoming: HistoryEvent[]) {
  const rows = new Map(existing.map((event) => [event.id, event]));
  for (const event of incoming) rows.set(event.id, event);
  return [...rows.values()].sort((a, b) =>
    BigInt(a.sequence) < BigInt(b.sequence)
      ? -1
      : BigInt(a.sequence) > BigInt(b.sequence)
        ? 1
        : 0,
  );
}

export function useRoomHistory(roomId?: string) {
  const current = useRef<Context | null>(null);
  const [view, setView] = useState<View>(() => empty(roomId));
  const update = useCallback((ctx: Context, patch: Partial<View>) => {
    if (current.current !== ctx) return;
    ctx.view = { ...ctx.view, ...patch };
    setView(ctx.view);
  }, []);
  const refresh = useCallback(
    (expectedRoomId?: string): Promise<void> => {
      const ctx = current.current;
      if (!ctx?.view.roomId) return Promise.resolve();
      if (expectedRoomId && ctx.view.roomId !== expectedRoomId)
        return Promise.resolve();
      if (ctx.refresh) {
        ctx.again = true;
        return ctx.refresh;
      }
      const request = async () => {
        do {
          ctx.again = false;
          update(ctx, { loading: !ctx.view.initialized, error: "" });
          try {
            if (!ctx.view.initialized) {
              const page = await api<HistoryPage>(
                `/rooms/${ctx.view.roomId}/events/page`,
              );
              if (current.current !== ctx) return;
              update(ctx, {
                events: page.events,
                nextCursor: page.nextCursor,
                initialized: true,
              });
            } else {
              let cursor: string | null =
                ctx.view.events.at(-1)?.sequence ?? "0";
              // Catch every event missed offline, even across multiple pages.
              while (cursor !== null) {
                const page: HistoryPage = await api<HistoryPage>(
                  `/rooms/${ctx.view.roomId}/events/page?after=${encodeURIComponent(cursor)}`,
                );
                if (current.current !== ctx) return;
                update(ctx, { events: merge(ctx.view.events, page.events) });
                cursor = page.nextCursor;
              }
            }
          } catch (error) {
            update(ctx, {
              error:
                error instanceof Error
                  ? error.message
                  : "Не удалось обновить историю",
            });
          } finally {
            update(ctx, { loading: false });
          }
        } while (ctx.again && current.current === ctx);
      };
      ctx.refresh = request().finally(() => {
        ctx.refresh = undefined;
      });
      return ctx.refresh;
    },
    [update],
  );
  useEffect(() => {
    const ctx: Context = { view: empty(roomId), again: false };
    current.current = ctx;
    setView(ctx.view);
    if (roomId) void refresh();
    return () => {
      if (current.current === ctx) current.current = null;
    };
  }, [roomId, refresh]);
  const loadOlder = useCallback(async () => {
    const ctx = current.current;
    if (!ctx?.view.nextCursor || ctx.view.loadingOlder) return;
    update(ctx, { loadingOlder: true, olderError: "" });
    try {
      const page = await api<HistoryPage>(
        `/rooms/${ctx.view.roomId}/events/page?before=${encodeURIComponent(ctx.view.nextCursor!)}`,
      );
      if (current.current !== ctx) return;
      update(ctx, {
        events: merge(ctx.view.events, page.events),
        nextCursor: page.nextCursor,
      });
    } catch (error) {
      update(ctx, {
        olderError:
          error instanceof Error
            ? error.message
            : "Не удалось загрузить ранние события",
      });
    } finally {
      update(ctx, { loadingOlder: false });
    }
  }, [update]);
  return {
    ...(view.roomId === roomId ? view : empty(roomId)),
    refresh,
    loadOlder,
  };
}
