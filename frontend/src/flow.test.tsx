import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { DeleteRoomDialog } from "./DeleteRoomDialog";
import { useRoomSocket } from "./useRoomSocket";
import type { Room } from "./types";
vi.mock("@twa-dev/sdk", () => ({
  default: { initData: "test-init-data", initDataUnsafe: {} },
}));
const room: Room = {
  id: "room-1",
  code: "ABC123",
  ownerId: 1,
  status: "WAITING",
  inviteUrl: "",
  members: [{ userId: 1, name: "Owner", role: "OWNER", ready: false }],
  state: {
    settings: {
      name: "Тестовая кампания",
      setting: "Fantasy",
      worldDescription: "",
      tone: "",
      rules: "",
      difficulty: "",
      gmStyle: "",
      ollamaModel: "test",
      maxPlayers: 2,
    },
    characters: [],
    npcs: [],
    quests: [],
    scene: null,
    combat: false,
    turn: 0,
  },
};
class FakeSocket {
  static OPEN = 1;
  static sockets: FakeSocket[] = [];
  readyState = 1;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onclose: ((e: { code: number; reason: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  sent: string[] = [];
  constructor() {
    FakeSocket.sockets.push(this);
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
beforeEach(() => {
  localStorage.clear();
  FakeSocket.sockets = [];
  vi.stubGlobal("WebSocket", FakeSocket);
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function backend(userId = 1) {
  let deleted = false;
  const fetcher = vi.fn(
    async (input: string | URL | Request, init?: RequestInit) => {
      const path = String(input);
      let data: unknown;
      if (path === "/api/auth/telegram")
        data = {
          user: { id: userId, firstName: "Tester", username: "" },
          token: "test-session",
        };
      else if (path === "/api/rooms")
        data = init?.method === "POST" ? room : deleted ? [] : [room];
      else if (path.endsWith("/events")) data = [];
      else if (init?.method === "DELETE") {
        deleted = true;
        data = { deleted: true };
      } else data = room;
      return { ok: true, status: 200, json: async () => data } as Response;
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}
describe("room deletion", () => {
  it("requires the correct code, supports cancellation and surfaces a busy room", async () => {
    const onConfirm = vi
      .fn()
      .mockRejectedValue(new Error("Дождись завершения хода"));
    const onCancel = vi.fn();
    render(
      <DeleteRoomDialog
        room={room}
        onCancel={onCancel}
        onConfirm={onConfirm}
      />,
    );
    const confirm = screen.getByRole("button", {
      name: "Удалить навсегда",
    }) as HTMLButtonElement;
    expect(confirm.disabled).toBe(true);
    await userEvent.type(
      screen.getByLabelText("Код подтверждения удаления"),
      "WRONG",
    );
    expect(confirm.disabled).toBe(true);
    expect(onConfirm).not.toHaveBeenCalled();
    await userEvent.clear(screen.getByLabelText("Код подтверждения удаления"));
    await userEvent.type(
      screen.getByLabelText("Код подтверждения удаления"),
      room.code,
    );
    await userEvent.click(confirm);
    expect(onConfirm).toHaveBeenCalledWith(room.code);
    expect(await screen.findByRole("alert")).toHaveProperty(
      "textContent",
      "Дождись завершения хода",
    );
    await userEvent.click(screen.getByRole("button", { name: "Отмена" }));
    expect(onCancel).toHaveBeenCalledOnce();
  });
  it("lets an owner delete a room and returns to the empty campaign list", async () => {
    const fetcher = backend();
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: /Тестовая кампания/ }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Удалить комнату" }),
    );
    await userEvent.type(
      screen.getByLabelText("Код подтверждения удаления"),
      room.code,
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Удалить навсегда" }),
    );
    await screen.findByText("Первая глава ещё не написана");
    expect(
      fetcher.mock.calls.some(
        ([path, init]) =>
          path === "/api/rooms/room-1" &&
          init?.method === "DELETE" &&
          init.body === JSON.stringify({ code: room.code }),
      ),
    ).toBe(true);
  });
  it("hides deletion for a player and exits on room_deleted", async () => {
    backend(2);
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: /Тестовая кампания/ }),
    );
    expect(screen.queryByRole("button", { name: "Удалить комнату" })).toBe(
      null,
    );
    await act(async () => {
      FakeSocket.sockets
        .at(-1)!
        .emit("room_deleted", { message: "Владелец удалил комнату" });
    });
    await screen.findByText("Владелец удалил комнату");
    expect(screen.getByText("Твои кампании")).toBeTruthy();
  });
});
it("ignores a late room refresh after deletion", async () => {
  const fetcher = backend();
  render(<App />);
  await userEvent.click(
    await screen.findByRole("button", { name: /Тестовая кампания/ }),
  );
  let release: (response: Response) => void = () => {};
  fetcher.mockImplementationOnce(
    () =>
      new Promise<Response>((resolve) => {
        release = resolve;
      }),
  );
  await act(async () => {
    FakeSocket.sockets.at(-1)!.emit("room_state", { refresh: true });
  });
  await act(async () => {
    FakeSocket.sockets.at(-1)!.emit("room_deleted", { message: "Удалено" });
  });
  await act(async () => {
    release({ ok: true, status: 200, json: async () => room } as Response);
  });
  expect(screen.getByText("Твои кампании")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Удалить комнату" })).toBe(null);
});
it("reconnects after an interruption but stops after deletion", async () => {
  vi.useFakeTimers();
  const removed = vi.fn(),
    refresh = vi.fn();
  function Harness() {
    const socket = useRoomSocket("one", refresh, vi.fn(), removed);
    return <span>{socket.status}</span>;
  }
  const view = render(<Harness />);
  const first = FakeSocket.sockets.at(-1)!;
  act(() => {
    first.onopen?.();
    first.emit("room_state", { refresh: true });
  });
  expect(first.sent[0]).toContain('"type":"auth"');
  act(() => {
    first.onclose?.({ code: 1006, reason: "" });
  });
  act(() => {
    vi.advanceTimersByTime(1000);
  });
  expect(FakeSocket.sockets.length).toBe(2);
  const second = FakeSocket.sockets.at(-1)!;
  act(() => {
    second.emit("room_deleted", { message: "Gone" });
  });
  act(() => {
    second.onclose?.({ code: 1000, reason: "room_deleted" });
    vi.advanceTimersByTime(30000);
  });
  expect(removed).toHaveBeenCalledOnce();
  expect(FakeSocket.sockets.length).toBe(2);
  view.unmount();
  vi.useRealTimers();
});

it("restores the processing player on reconnect and clears it when the turn ends", async () => {
  const refresh = vi.fn();
  function Harness() {
    const socket = useRoomSocket("one", refresh, vi.fn(), vi.fn());
    return (
      <span>
        {socket.status} · {socket.processing ? socket.actor : "Свободно"}
      </span>
    );
  }
  render(<Harness />);
  const ws = FakeSocket.sockets.at(-1)!;
  act(() => {
    ws.emit("room_state", {
      refresh: true,
      processing: true,
      userId: 2,
      name: "Анна",
    });
  });
  expect(screen.getByText("online · Анна")).toBeTruthy();
  expect(refresh).toHaveBeenCalledOnce();
  act(() => {
    ws.emit("room_activity", { processing: false });
  });
  expect(screen.getByText("online · Свободно")).toBeTruthy();
});

describe("adventure onboarding", () => {
  function setup(initial: Room, userId = 1) {
    let current = initial;
    const fetcher = vi.fn(async (input: string | URL | Request) => {
      const path = String(input);
      const data =
        path === "/api/auth/telegram"
          ? {
              user: { id: userId, firstName: "Tester", username: "" },
              token: "test",
            }
          : path === "/api/rooms"
            ? [current]
            : path.endsWith("/events")
              ? [
                  {
                    id: "intro",
                    type: "GM_MESSAGE",
                    payload: { text: "Фонарь погас. Что вы делаете?" },
                  },
                ]
              : current;
      return { ok: true, status: 200, json: async () => data } as Response;
    });
    vi.stubGlobal("fetch", fetcher);
    return {
      fetcher,
      update: (next: Room) => {
        current = next;
      },
    };
  }

  it("confirms finishing, sends a tracked command and shows saved results", async () => {
    const current = structuredClone(room);
    current.status = "PLAYING";
    current.state.turn = 9;
    current.state.quests = [
      { id: "quest", title: "Спасти мост", description: "", status: "ACTIVE" },
    ];
    const mock = setup(current);
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: /Тестовая кампания/ }),
    );
    const socket = FakeSocket.sockets.at(-1)!;
    await act(async () => {
      socket.emit("room_state", { refresh: true });
    });
    await userEvent.click(
      screen.getByRole("button", { name: "Завершить приключение" }),
    );
    expect(
      (
        screen.getByRole("button", {
          name: "Завершить и сохранить",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await userEvent.click(screen.getByRole("button", { name: "Отмена" }));
    expect(socket.sent).toHaveLength(0);
    await userEvent.click(
      screen.getByRole("button", { name: "Завершить приключение" }),
    );
    await userEvent.type(
      screen.getByLabelText("Код подтверждения завершения"),
      current.code,
    );
    await userEvent.type(
      screen.getByLabelText("Итог истории (необязательно)"),
      "Вернулись домой.",
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Завершить и сохранить" }),
    );
    expect(screen.queryByRole("dialog")).toBe(null);
    const sent = JSON.parse(socket.sent.at(-1)!);
    expect(sent).toEqual({
      id: expect.any(String),
      type: "finish_game",
      expectedTurn: 9,
      data: { code: current.code, text: "Вернулись домой." },
    });
    const finished = structuredClone(current);
    finished.status = "FINISHED";
    finished.state.ending = {
      reason: "owner",
      note: "Вернулись домой.",
      finishedAt: new Date().toISOString(),
    };
    mock.update(finished);
    await act(async () => {
      socket.emit("command_status", { id: sent.id, status: "completed" });
    });
    expect(
      await screen.findByRole("heading", { name: "Приключение завершено" }),
    ).toBeTruthy();
    expect(screen.getByText("Вернулись домой.")).toBeTruthy();
    expect(screen.getByText(/незавершено 1/)).toBeTruthy();
    expect(screen.queryByLabelText("Действие персонажа")).toBe(null);
    expect(
      screen.queryByRole("button", { name: "Завершить приключение" }),
    ).toBe(null);
    expect(screen.getByText("Фонарь погас. Что вы делаете?")).toBeTruthy();
    await userEvent.click(
      screen.getByRole("button", { name: "К списку кампаний" }),
    );
    expect(screen.getByText("Твои кампании")).toBeTruthy();
  });

  it("hides finishing for non-owners", async () => {
    const current = structuredClone(room);
    current.status = "PAUSED";
    setup(current, 2);
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: /Тестовая кампания/ }),
    );
    expect(
      screen.queryByRole("button", { name: "Завершить приключение" }),
    ).toBe(null);
  });

  it("opens legacy defeated campaigns as readable results", async () => {
    const current = structuredClone(room);
    current.status = "FINISHED";
    current.state.characters = [
      {
        id: "hero",
        userId: 1,
        name: "Tester",
        race: "Человек",
        class: "Воин",
        level: 1,
        hp: 0,
        maxHp: 20,
        armorClass: 13,
        stats: {},
        inventory: [],
      },
    ];
    setup(current);
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: /Тестовая кампания/ }),
    );
    expect(
      await screen.findByRole("heading", { name: "Отряд пал" }),
    ).toBeTruthy();
    expect(screen.getByText(/Выжило героев: 0\/1/)).toBeTruthy();
    expect(screen.queryByLabelText("Действие персонажа")).toBe(null);
    await userEvent.click(screen.getByRole("button", { name: "Отряд" }));
    expect(screen.queryByRole("button", { name: "Продолжить кампанию" })).toBe(
      null,
    );
  });

  it("shows the current fighter and prevents acting out of turn", async () => {
    const current = structuredClone(room);
    current.status = "PLAYING";
    current.state.turn = 7;
    current.state.combat = true;
    current.state.combatOrder = [2, 1];
    current.state.combatIndex = 0;
    current.state.combatRound = 2;
    current.state.combatTurnSince = new Date(Date.now() - 120000).toISOString();
    current.state.characters = [1, 2].map((id) => ({
      id: `hero-${id}`,
      userId: id,
      name: id === 1 ? "Tester" : "Анна",
      race: "Человек",
      class: "Воин",
      level: 1,
      hp: 20,
      maxHp: 20,
      armorClass: 13,
      stats: {},
      inventory: [],
    }));
    const mock = setup(current);
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: /Тестовая кампания/ }),
    );
    const socket = FakeSocket.sockets.at(-1)!;
    await act(async () => {
      socket.emit("room_state", { refresh: true, processing: false });
    });
    expect(screen.getByText("Раунд 2 · Ходит Анна")).toBeTruthy();
    expect(
      (screen.getByLabelText("Действие персонажа") as HTMLTextAreaElement)
        .disabled,
    ).toBe(true);
    expect(
      (
        screen.getByRole("button", {
          name: "Отправить действие",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    await userEvent.click(
      screen.getByRole("button", { name: "Пропустить ход: Анна" }),
    );
    expect(socket.sent.map((v) => JSON.parse(v))).toContainEqual(
      expect.objectContaining({
        type: "skip_turn",
        data: { turn: 7 },
      }),
    );
    const next = structuredClone(current);
    next.state.combatIndex = 1;
    next.state.turn = 8;
    mock.update(next);
    await act(async () => {
      socket.emit("command_status", {
        id: JSON.parse(socket.sent.at(-1)!).id,
        status: "completed",
      });
      socket.emit("room_state", { refresh: true });
    });
    expect(await screen.findByText("Раунд 2 · Ходит Tester")).toBeTruthy();
    expect(
      (screen.getByLabelText("Действие персонажа") as HTMLTextAreaElement)
        .disabled,
    ).toBe(false);
    await userEvent.click(
      screen.getByRole("button", { name: "Пропустить мой ход" }),
    );
    expect(socket.sent.map((v) => JSON.parse(v))).toContainEqual(
      expect.objectContaining({
        type: "pass_turn",
        data: {},
      }),
    );
  });

  it("creates a prepared adventure without a settings form", async () => {
    const fetcher = backend();
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: "Быстрое приключение" }),
    );
    const call = fetcher.mock.calls.find(
      ([path, init]) => path === "/api/rooms" && init?.method === "POST",
    );
    const settings = JSON.parse(call![1]!.body as string);
    expect(settings.name).toBe("Последний фонарь");
    expect(settings.worldDescription).toContain("огненный камень");
    expect(settings.ollamaModel).toBe("");
    expect(await screen.findByLabelText("Подготовка отряда")).toBeTruthy();
  });

  it("blocks start until every member has a hero and confirms readiness", async () => {
    const current = structuredClone(room);
    current.members.push({
      userId: 2,
      name: "Анна",
      role: "PLAYER",
      ready: false,
    });
    current.state.characters.push({
      id: "hero-1",
      userId: 1,
      name: "Tester",
      race: "Человек",
      class: "Воин",
      level: 1,
      hp: 20,
      maxHp: 20,
      armorClass: 13,
      stats: {},
      inventory: [],
    });
    const mock = setup(current);
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: /Тестовая кампания/ }),
    );
    const socket = FakeSocket.sockets.at(-1)!;
    await act(async () => {
      socket.emit("room_state", { refresh: true });
    });
    expect(
      (
        screen.getByRole("button", {
          name: "Начать приключение",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(screen.getByText(/Анна — создать героя/)).toBeTruthy();
    await userEvent.click(
      screen.getByRole("button", { name: "Я готов к приключению" }),
    );
    expect(socket.sent.map((s) => JSON.parse(s))).toContainEqual(
      expect.objectContaining({
        type: "ready",
        data: { ready: true },
      }),
    );
    const ready = structuredClone(current);
    ready.members.forEach((m) => {
      m.ready = true;
    });
    ready.state.characters.push({
      ...ready.state.characters[0],
      id: "hero-2",
      userId: 2,
      name: "Анна",
    });
    mock.update(ready);
    await act(async () => {
      socket.emit("command_status", {
        id: JSON.parse(socket.sent.at(-1)!).id,
        status: "completed",
      });
      socket.emit("room_state", { refresh: true });
    });
    await waitFor(() =>
      expect(
        (
          screen.getByRole("button", {
            name: "Начать приключение",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(false),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "Начать приключение" }),
    );
    expect(socket.sent.map((s) => JSON.parse(s))).toContainEqual(
      expect.objectContaining({
        type: "start_game",
        data: {},
      }),
    );
    await act(async () => {
      socket.emit("game_status_changed", { processing: true });
    });
    expect(screen.getByText("Мастер готовит вступление…")).toBeTruthy();
    await act(async () => {
      socket.emit("command_status", {
        id: JSON.parse(socket.sent.at(-1)!).id,
        status: "failed",
        message: "Не удалось создать вступление",
      });
      socket.emit("game_status_changed", { processing: false });
    });
    expect(screen.getByText("Не удалось создать вступление")).toBeTruthy();
    await userEvent.click(
      screen.getByRole("button", { name: "Открыть отряд" }),
    );
    expect(
      (
        screen.getByRole("button", {
          name: "Начать приключение",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });

  it("shows the opening goal and lets a player edit an example before sending", async () => {
    const current = structuredClone(room);
    current.status = "PLAYING";
    current.state.scene = {
      title: "Мост",
      description: "Вокруг туман",
      location: "Мост",
    };
    current.state.quests = [
      {
        id: "quest",
        title: "Вернуть свет",
        description: "Найти огненный камень",
        status: "ACTIVE",
      },
    ];
    current.state.characters = [
      {
        id: "hero",
        userId: 1,
        name: "Tester",
        race: "Человек",
        class: "Воин",
        level: 1,
        hp: 20,
        maxHp: 20,
        armorClass: 13,
        stats: {},
        inventory: [],
      },
    ];
    setup(current);
    render(<App />);
    await userEvent.click(
      await screen.findByRole("button", { name: /Тестовая кампания/ }),
    );
    expect(
      await screen.findByText("Фонарь погас. Что вы делаете?"),
    ).toBeTruthy();
    expect(screen.getByText("Найти огненный камень")).toBeTruthy();
    const example = "Я осматриваюсь и ищу, что поможет нам достичь цели.";
    await userEvent.click(screen.getByRole("button", { name: example }));
    const input = screen.getByLabelText(
      "Действие персонажа",
    ) as HTMLTextAreaElement;
    expect(input.value).toBe(example);
    await userEvent.type(input, " Проверяю мост.");
    expect(input.value).toContain("Проверяю мост.");
    expect(FakeSocket.sockets.at(-1)!.sent).toHaveLength(0);
  });
});
