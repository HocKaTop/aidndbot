import { useEffect, useRef, useState } from "react";
import type { Room } from "./types";

export function DeleteRoomDialog({
  room,
  onCancel,
  onConfirm,
}: {
  room: Room;
  onCancel: () => void;
  onConfirm: (code: string) => Promise<void>;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  return (
    <dialog
      ref={dialog}
      className="delete-dialog"
      aria-labelledby="delete-title"
      onCancel={(e) => {
        e.preventDefault();
        if (!busy) onCancel();
      }}
    >
      <h2 id="delete-title">Удалить комнату?</h2>
      <p>
        Кампания «{room.state.settings.name}», все её персонажи, предметы и
        история будут удалены для всех участников. Отменить это действие нельзя.
      </p>
      <form
        className="stack"
        onSubmit={async (e) => {
          e.preventDefault();
          if (busy || code.trim().toUpperCase() !== room.code) return;
          setBusy(true);
          setError("");
          try {
            await onConfirm(code.trim().toUpperCase());
          } catch (e) {
            setError(
              e instanceof Error ? e.message : "Не удалось удалить комнату",
            );
          } finally {
            setBusy(false);
          }
        }}
      >
        <label>
          Для подтверждения введи код {room.code}
          <input
            autoFocus
            aria-label="Код подтверждения удаления"
            value={code}
            disabled={busy}
            autoComplete="off"
            onChange={(e) => setCode(e.target.value)}
          />
        </label>
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <div className="two">
          <button type="button" disabled={busy} onClick={onCancel}>
            Отмена
          </button>
          <button
            className="danger"
            disabled={busy || code.trim().toUpperCase() !== room.code}
          >
            {busy ? "Удаляем…" : "Удалить навсегда"}
          </button>
        </div>
      </form>
    </dialog>
  );
}
