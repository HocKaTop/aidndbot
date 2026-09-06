import { useCallback, useEffect, useRef, useState } from "react";
import { getToken } from "./api";
export function useRoomSocket(
  roomId: string | undefined,
  refresh: () => void,
  onError: (message: string) => void,
) {
  const socket = useRef<WebSocket | null>(null);
  const callbacks = useRef({ refresh, onError });
  callbacks.current = { refresh, onError };
  const [status, setStatus] = useState("offline");
  const [processing, setProcessing] = useState(false);
  useEffect(() => {
    if (!roomId) {
      setStatus("offline");
      setProcessing(false);
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
          const frame = JSON.parse(event.data);
          if (frame.type === "error") {
            setProcessing(false);
            callbacks.current.onError(frame.data.message);
            return;
          }
          if (frame.data?.processing !== undefined) {
            setProcessing(frame.data.processing);
            return;
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
        if (event.code === 1008) {
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
  return { status, processing, send };
}
