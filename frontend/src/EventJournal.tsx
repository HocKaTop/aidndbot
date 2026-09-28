import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Dices, Sparkles } from "lucide-react";
import type { useRoomHistory } from "./useRoomHistory";

export function EventJournal({ history }: { history: ReturnType<typeof useRoomHistory> }) {
  const { events } = history;
  const root = useRef<HTMLDivElement>(null);
  const bottom = useRef<HTMLDivElement>(null);
  const following = useRef(true);
  const anchor = useRef<{ id: string; top: number } | null>(null);
  const [unread, setUnread] = useState(false);
  useEffect(() => {
    const onScroll = () => {
      following.current =
        (bottom.current?.getBoundingClientRect().top ?? Infinity) <=
        window.innerHeight + 120;
      if (following.current) setUnread(false);
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);
  useLayoutEffect(() => {
    if (!anchor.current || history.loadingOlder) return;
    const element = [
      ...(root.current?.querySelectorAll<HTMLElement>("[data-event-id]") ?? []),
    ].find((el) => el.dataset.eventId === anchor.current!.id);
    if (element)
      window.scrollBy(
        0,
        element.getBoundingClientRect().top - anchor.current.top,
      );
    anchor.current = null;
  }, [events[0]?.id, history.loadingOlder]);
  useLayoutEffect(() => {
    if (!events.length) return;
    if (
      following.current &&
      !anchor.current &&
      document.activeElement?.tagName !== "TEXTAREA"
    )
      bottom.current?.scrollIntoView({ block: "nearest" });
    else setUnread(true);
  }, [events.at(-1)?.id]);
  const older = async () => {
    const first = root.current?.querySelector<HTMLElement>("[data-event-id]");
    if (first)
      anchor.current = {
        id: first.dataset.eventId!,
        top: first.getBoundingClientRect().top,
      };
    following.current = false;
    setUnread(true);
    await history.loadOlder();
  };
  return (
    <div className="journal" ref={root} aria-label="История кампании">
      {history.loading && <p role="status">Загружаем историю…</p>}
      {history.error && (
        <div role="alert">
          <p>{history.error}</p>
          <button onClick={() => void history.refresh()}>
            Повторить загрузку истории
          </button>
        </div>
      )}
      {history.nextCursor && (
        <button
          className="secondary full"
          disabled={history.loadingOlder}
          onClick={() => void older()}
        >
          {history.loadingOlder ? "Загружаем…" : "Загрузить ранние события"}
        </button>
      )}
      {history.olderError && <p role="alert">{history.olderError}</p>}
      {history.initialized && !history.nextCursor && (
        <p className="eyebrow">
          {events.length ? "Начало истории" : "Событий пока нет"}
        </p>
      )}
      {unread && (
        <button
          className="secondary full journal-latest"
          onClick={() => {
            following.current = true;
            setUnread(false);
            bottom.current?.scrollIntoView({
              behavior: "smooth",
              block: "end",
            });
          }}
        >
          К последним событиям
        </button>
      )}
      {events.map((event) => (
        <article
          key={event.id}
          data-event-id={event.id}
          className={
            "event " +
            (event.type === "GM_MESSAGE"
              ? "gm"
              : event.type === "PLAYER_ACTION"
                ? "player"
                : "mechanics")
          }
        >
          <div className="eyebrow">
            {event.type === "GM_MESSAGE" ? (
              <>
                <Sparkles size={13} />
                Ведущий
              </>
            ) : event.type === "PLAYER_ACTION" ? (
              "Действие игрока"
            ) : (
              <>
                <Dices size={13} />
                Правила мира
              </>
            )}
          </div>
          <p>{event.payload.text}</p>
        </article>
      ))}
      <div ref={bottom} />
    </div>
  );
}
