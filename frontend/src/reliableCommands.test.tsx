import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import { useRoomSocket } from "./useRoomSocket";

class Socket {
  static OPEN = 1;
  static all: Socket[] = [];
  readyState = 1;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: ((e: { code: number; reason: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor() {
    Socket.all.push(this);
  }
  send(data: string) {
    this.sent.push(data);
  }
  close() {
    this.readyState = 3;
  }
  emit(type: string, data: unknown) {
    this.onmessage?.({ data: JSON.stringify({ type, data }) });
  }
}
const latest = () => Socket.all.at(-1)!;
const packet = () => JSON.parse(latest().sent.at(-1)!);
function Harness({
  room = "room-a",
  user = 1,
  turn = 3,
}: {
  room?: string;
  user?: number;
  turn?: number;
}) {
  const s = useRoomSocket(room, vi.fn(), vi.fn(), vi.fn(), user, turn);
  return (
    <>
      <input
        aria-label="draft"
        value={s.draft}
        onChange={(e) => s.setDraft(e.target.value)}
      />
      <span data-testid="state">{s.delivery?.status || "idle"}</span>
      <button
        onClick={() => {
          s.send("player_action", { text: s.draft });
          s.send("player_action", { text: s.draft });
        }}
      >
        Send twice
      </button>
      <button onClick={s.retry}>Retry</button>
    </>
  );
}
const edit = (text: string) =>
  fireEvent.change(screen.getByLabelText("draft"), { target: { value: text } });
const text = () => (screen.getByLabelText("draft") as HTMLInputElement).value;
const connect = () => act(() => latest().emit("room_state", { refresh: true }));
beforeEach(() => {
  localStorage.clear();
  Socket.all = [];
  vi.stubGlobal("WebSocket", Socket);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

it("retains text until a matching completed receipt and prevents double submission", () => {
  render(<Harness />);
  connect();
  edit("Осматриваю дверь");
  fireEvent.click(screen.getByText("Send twice"));
  const sent = packet();
  expect(latest().sent).toHaveLength(1);
  expect(sent.expectedTurn).toBe(3);
  expect(text()).toBe("Осматриваю дверь");
  for (const status of ["queued", "processing"]) {
    act(() => latest().emit("command_status", { id: sent.id, status }));
    expect(screen.getByTestId("state").textContent).toBe(status);
    expect(text()).toBe("Осматриваю дверь");
  }
  edit("Изменённый текст");
  expect(text()).toBe("Осматриваю дверь");
  act(() =>
    latest().emit("command_status", { id: "another-id", status: "completed" }),
  );
  expect(text()).toBe("Осматриваю дверь");
  act(() =>
    latest().emit("command_status", { id: sent.id, status: "completed" }),
  );
  expect(text()).toBe("");
  edit("Следующее действие");
  act(() =>
    latest().emit("command_status", { id: sent.id, status: "completed" }),
  );
  expect(text()).toBe("Следующее действие");
});

it("recovers a lost acknowledgement after reloading without automatically replaying the action", () => {
  const view = render(<Harness />);
  connect();
  edit("Открываю сундук");
  fireEvent.click(screen.getByText("Send twice"));
  const sent = packet();
  view.unmount();
  render(<Harness turn={4} />);
  connect();
  expect(text()).toBe("Открываю сундук");
  expect(packet()).toEqual({ type: "command_status", id: sent.id });
  expect(latest().sent).toHaveLength(1);
  act(() =>
    latest().emit("command_status", { id: sent.id, status: "completed" }),
  );
  expect(text()).toBe("");
  expect(localStorage.getItem("nocturna:draft:1:room-a")).toBe(null);
});

it("retries an unknown outcome with the original ID and turn, then allows editing a rejected action", () => {
  const view = render(<Harness />);
  connect();
  edit("Беру предмет");
  fireEvent.click(screen.getByText("Send twice"));
  const sent = packet();
  view.unmount();
  render(<Harness turn={9} />);
  connect();
  act(() =>
    latest().emit("command_status", { id: sent.id, status: "unknown" }),
  );
  fireEvent.click(screen.getByText("Retry"));
  expect(packet()).toEqual(sent);
  act(() =>
    latest().emit("command_status", {
      id: sent.id,
      status: "failed",
      message: "Ситуация изменилась",
    }),
  );
  expect(text()).toBe("Беру предмет");
  edit("Осматриваюсь");
  fireEvent.click(screen.getByText("Send twice"));
  expect(packet().id).not.toBe(sent.id);
  expect(packet().expectedTurn).toBe(9);
  expect(packet().data.text).toBe("Осматриваюсь");
});

it("keeps drafts separate across rooms and users and ignores late replies from a previous room", () => {
  const view = render(<Harness />);
  connect();
  edit("Комната A");
  fireEvent.click(screen.getByText("Send twice"));
  const sent = packet(),
    old = latest();
  view.rerender(<Harness room="room-b" />);
  connect();
  expect(text()).toBe("");
  edit("Комната B");
  act(() => old.emit("command_status", { id: sent.id, status: "completed" }));
  expect(text()).toBe("Комната B");
  view.rerender(<Harness room="room-a" user={2} />);
  connect();
  expect(text()).toBe("");
  view.rerender(<Harness />);
  connect();
  expect(text()).toBe("Комната A");
  act(() => latest().emit("room_deleted", { message: "Удалено" }));
  expect(localStorage.getItem("nocturna:draft:1:room-a")).toBe(null);
});

it("queries status after reconnecting and marks an unacknowledged action uncertain", () => {
  vi.useFakeTimers();
  render(<Harness />);
  connect();
  edit("Жду у двери");
  fireEvent.click(screen.getByText("Send twice"));
  const sent = packet();
  act(() => vi.advanceTimersByTime(20000));
  expect(screen.getByTestId("state").textContent).toBe("unknown");
  expect(packet()).toEqual({ type: "command_status", id: sent.id });
  act(() => latest().onclose?.({ code: 1006, reason: "" }));
  act(() => vi.advanceTimersByTime(1000));
  connect();
  expect(latest().sent.map((s) => JSON.parse(s))).toEqual([
    { type: "command_status", id: sent.id },
  ]);
  expect(text()).toBe("Жду у двери");
});
