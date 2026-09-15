import { useCallback, useEffect, useRef, useState } from "react";
import { getToken } from "./api";
export function useRoomSocket(
  roomId: string | undefined,
  refresh: () => void,
  onError: (message: string) => void,
  onRemoved: (message: string) => void,
) {
  const socket = useRef<WebSocket | null>(null);
  const callbacks = useRef({ refresh, onError, onRemoved });
  callbacks.current = { refresh, onError, onRemoved };
  const [status, setStatus] = useState("offline");
  const [processing, setProcessing] = useState(false);
  const [actor, setActor] = useState("");
  useEffect(() => {
    if (!roomId) {
      setStatus("offline");
      setProcessing(false);
      setActor("");
      return;
    }
    let closed = false,
      timer: ReturnType<typeof setTimeout>,
      attempt = 0;
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
          if (frame.type === "error") {
            callbacks.current.onError(frame.data.message);
            return;
          }
          if (frame.data?.processing !== undefined) {
            setProcessing(frame.data.processing);
            setActor(frame.data.processing ? frame.data.name || "" : "");
          }
          if (frame.data?.refresh) {
            attempt = 0;
            setStatus("online");
            callbacks.current.refresh();
          }
        } catch {
          callbacks.current.onError("Не удалось прочитать событие сервера");
        }
      };
      ws.onclose = (event) => {
        if (closed) return;
        setStatus("offline");
        setProcessing(false);
        setActor("");
        if (event.reason === "room_deleted" || event.reason === "room_left") {
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
      clearTimeout(timer);
      socket.current?.close();
      socket.current = null;
    };
  }, [roomId]);
  const send = useCallback(
    (type: string, data: unknown) => {
      if (
        socket.current?.readyState !== WebSocket.OPEN ||
        status !== "online"
      ) {
        callbacks.current.onError("Соединение восстанавливается");
        return false;
      }
      socket.current.send(JSON.stringify({ type, data }));
      return true;
    },
    [status],
  );
  return { status, processing, actor, send };
}
