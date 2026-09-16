import { useCallback, useEffect, useRef, useState } from "react";
import { getToken } from "./api";
import {
  loadDraft,
  saveDraft,
  unresolved,
  type Draft,
  type DeliveryState,
  type Packet,
} from "./commandDraft";
export function useRoomSocket(
  roomId: string | undefined,
  refresh: () => void,
  onError: (message: string) => void,
  onRemoved: (message: string) => void,
  userId?: number,
  turn?: number,
) {
  const key = roomId && userId ? `nocturna:draft:${userId}:${roomId}` : "";
  const [draft, setDraftState] = useState<Draft>(() => loadDraft(key));
  const current = useRef(draft);
  const online = useRef(false);
  const feedbackAt = useRef(Date.now());
  const update = useCallback((next: Draft) => {
    current.current = next;
    saveDraft(next);
    setDraftState(next);
  }, []);
  const setDraft = useCallback(
    (text: string) => {
      if (current.current.key !== key || unresolved(current.current.delivery))
        return;
      update({ ...current.current, text });
    },
    [key, update],
  );
  const socket = useRef<WebSocket | null>(null);
  const callbacks = useRef({ refresh, onError, onRemoved });
  callbacks.current = { refresh, onError, onRemoved };
  const [status, setStatus] = useState("offline");
  const [processing, setProcessing] = useState(false);
  const [actor, setActor] = useState("");
  useEffect(() => {
    online.current = false;
    update(loadDraft(key));
    const query = () => {
      const pending = current.current.delivery;
      if (
        online.current &&
        socket.current?.readyState === WebSocket.OPEN &&
        unresolved(pending)
      ) {
        try {
          socket.current.send(
            JSON.stringify({ type: "command_status", id: pending!.packet.id }),
          );
        } catch {
          /* Reconnection will check again. */
        }
      }
    };
    const markUnknown = () => {
      if (unresolved(current.current.delivery))
        update({
          ...current.current,
          delivery: {
            ...current.current.delivery!,
            status: "unknown",
            message:
              "Подтверждение не получено. Проверь результат или повтори отправку.",
          },
        });
    };
    if (!roomId) {
      setStatus("offline");
      setProcessing(false);
      setActor("");
      return;
    }
    let closed = false,
      timer: ReturnType<typeof setTimeout>,
      attempt = 0;
    const checkTimer = setInterval(() => {
      if (
        unresolved(current.current.delivery) &&
        Date.now() - feedbackAt.current > 15000
      )
        markUnknown();
      query();
    }, 5000);
    const connect = () => {
      if (closed) return;
      setStatus("connecting");
      const ws = new WebSocket(
        `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/ws/rooms/${roomId}`,
      );
      socket.current = ws;
      ws.onopen = () => {
        ws.send(JSON.stringify({ type: "auth", data: { token: getToken() } }));
      };
      ws.onmessage = (event) => {
        try {
          if (closed) return;
          const frame = JSON.parse(event.data);
          if (frame.type === "room_deleted" || frame.type === "room_left") {
            update({ key, text: "", delivery: null });
            closed = true;
            setStatus("offline");
            setProcessing(false);
            setActor("");
            ws.close();
            callbacks.current.onRemoved(
              frame.data?.message || "Комната недоступна",
            );
            return;
          }
          if (frame.type === "command_status") {
            const delivery = current.current.delivery;
            const nextStatus = frame.data?.status as DeliveryState;
            if (
              !delivery ||
              frame.data?.id !== delivery.packet.id ||
              delivery.status === "completed" ||
              (delivery.status === "failed" && nextStatus !== "completed") ||
              ![
                "queued",
                "processing",
                "unknown",
                "failed",
                "completed",
              ].includes(nextStatus)
            )
              return;
            feedbackAt.current = Date.now();
            const text =
              nextStatus === "completed" &&
              delivery.packet.type === "player_action" &&
              current.current.text === delivery.packet.data.text
                ? ""
                : current.current.text;
            update({
              ...current.current,
              text,
              delivery: {
                ...delivery,
                status: nextStatus,
                message: frame.data.message,
              },
            });
            if (nextStatus === "completed") callbacks.current.refresh();
            return;
          }
          if (frame.type === "error") {
            callbacks.current.onError(frame.data.message);
            return;
          }
          if (frame.data?.processing !== undefined) {
            setProcessing(frame.data.processing);
            setActor(frame.data.processing ? frame.data.name || "" : "");
          }
          if (frame.data?.refresh) {
            const first = !online.current;
            online.current = true;
            attempt = 0;
            setStatus("online");
            callbacks.current.refresh();
            if (first) query();
          }
        } catch {
          callbacks.current.onError("Не удалось прочитать событие сервера");
        }
      };
      ws.onclose = (event) => {
        if (closed) return;
        online.current = false;
        markUnknown();
        setStatus("offline");
        setProcessing(false);
        setActor("");
        if (event.reason === "room_deleted" || event.reason === "room_left") {
          update({ key, text: "", delivery: null });
          closed = true;
          callbacks.current.onRemoved("Комната удалена или больше недоступна");
          return;
        }
        if (event.code === 1008) {
          callbacks.current.refresh();
          callbacks.current.onError(
            "Нет доступа или сессия истекла. Открой приложение заново через Telegram.",
          );
          return;
        }
        timer = setTimeout(connect, Math.min(1000 * 2 ** attempt++, 15000));
      };
      ws.onerror = () => ws.close();
    };
    connect();
    return () => {
      closed = true;
      online.current = false;
      clearTimeout(timer);
      clearInterval(checkTimer);
      socket.current?.close();
      socket.current = null;
    };
  }, [roomId, key, update]);
  const send = useCallback(
    (type: string, data: Record<string, unknown>) => {
      if (current.current.key !== key || unresolved(current.current.delivery))
        return false;
      if (
        socket.current?.readyState !== WebSocket.OPEN ||
        status !== "online"
      ) {
        callbacks.current.onError("Соединение восстанавливается");
        return false;
      }
      const packet: Packet = { id: crypto.randomUUID(), type, data };
      if (
        turn !== undefined &&
        [
          "player_action",
          "use_item",
          "pass_turn",
          "skip_turn",
          "finish_game",
        ].includes(type)
      )
        packet.expectedTurn = turn;
      feedbackAt.current = Date.now();
      update({ ...current.current, delivery: { packet, status: "sent" } });
      try {
        socket.current.send(JSON.stringify(packet));
      } catch {
        update({
          ...current.current,
          delivery: {
            packet,
            status: "unknown",
            message:
              "Соединение прервалось. Повтори отправку после подключения.",
          },
        });
      }
      return true;
    },
    [status, key, turn, update],
  );
  const retry = useCallback(() => {
    const delivery = current.current.delivery;
    if (
      current.current.key !== key ||
      !delivery ||
      !["failed", "unknown"].includes(delivery.status) ||
      !online.current ||
      socket.current?.readyState !== WebSocket.OPEN
    )
      return;
    if (
      delivery.packet.type === "player_action" &&
      current.current.text !== delivery.packet.data.text
    )
      return;
    feedbackAt.current = Date.now();
    update({
      ...current.current,
      delivery: { ...delivery, status: "sent", message: undefined },
    });
    try {
      socket.current.send(JSON.stringify(delivery.packet));
    } catch {
      update({
        ...current.current,
        delivery: {
          ...delivery,
          status: "unknown",
          message: "Не удалось подтвердить отправку.",
        },
      });
    }
  }, [key, update]);
  const visible = draft.key === key ? draft : { text: "", delivery: null };
  return {
    status,
    processing,
    actor,
    send,
    draft: visible.text,
    setDraft,
    delivery: visible.delivery,
    pending: unresolved(visible.delivery),
    retry,
  };
}
