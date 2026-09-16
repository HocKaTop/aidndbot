import { useEffect, useRef, useState } from "react";
import type { Room } from "./types";

export function CampaignEnding({
  room,
  onHome,
}: {
  room: Room;
  onHome: () => void;
}) {
  const { ending, characters, quests, turn } = room.state;
  const alive = characters.filter((h) => h.hp > 0).length;
  const defeated =
    ending?.reason === "defeat" ||
    (!ending && characters.length > 0 && alive === 0);
  return (
    <section className="panel stack" aria-label="Итоги приключения">
      <div className="eyebrow">Итоги приключения</div>
      <h2>{defeated ? "Отряд пал" : "Приключение завершено"}</h2>
      <p>{ending?.note || "История этого приключения сохранена."}</p>
      <p>
        Ходов: {turn} · Выжило героев: {alive}/{characters.length}
      </p>
      <p>
        Квесты: выполнено{" "}
        {quests.filter((q) => q.status === "COMPLETED").length}, провалено{" "}
        {quests.filter((q) => q.status === "FAILED").length}, незавершено{" "}
        {quests.filter((q) => q.status === "ACTIVE").length}.
      </p>
      <p>
        Можно перечитать события, посмотреть героев и квесты. Для новой истории
        создай отдельную кампанию.
      </p>
      <button className="primary" onClick={onHome}>
        К списку кампаний
      </button>
    </section>
  );
}

export function FinishCampaignDialog({
  room,
  disabled,
  onCancel,
  onConfirm,
}: {
  room: Room;
  disabled: boolean;
  onCancel: () => void;
  onConfirm: (code: string, note: string) => boolean;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [code, setCode] = useState("");
  const [note, setNote] = useState("");
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  return (
    <dialog
      ref={dialog}
      className="delete-dialog"
      aria-labelledby="finish-title"
      onCancel={(event) => {
        event.preventDefault();
        onCancel();
      }}
    >
      <h2 id="finish-title">Завершить приключение?</h2>
      <p>
        Кампания «{room.state.settings.name}» завершится для всего отряда. Герои
        и история сохранятся, но продолжить эту кампанию будет нельзя. Если
        нужен перерыв, поставь игру на паузу.
      </p>
      <form
        className="stack"
        onSubmit={(event) => {
          event.preventDefault();
          if (
            !disabled &&
            code.trim().toUpperCase() === room.code &&
            onConfirm(code.trim().toUpperCase(), note.trim())
          )
            onCancel();
        }}
      >
        <label>
          Итог истории (необязательно)
          <textarea
            maxLength={1000}
            value={note}
            onChange={(event) => setNote(event.target.value)}
          />
        </label>
        <label>
          Для подтверждения введи код {room.code}
          <input
            autoFocus
            autoComplete="off"
            aria-label="Код подтверждения завершения"
            value={code}
            onChange={(event) => setCode(event.target.value)}
          />
        </label>
        {disabled && (
          <p role="status">
            Дождись соединения и завершения текущего действия.
          </p>
        )}
        <div className="two">
          <button type="button" onClick={onCancel}>
            Отмена
          </button>
          <button
            className="primary"
            disabled={disabled || code.trim().toUpperCase() !== room.code}
          >
            Завершить и сохранить
          </button>
        </div>
      </form>
    </dialog>
  );
}
