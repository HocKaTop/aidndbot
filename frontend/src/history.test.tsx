import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react";
import { beforeEach, afterEach, expect, it, vi } from "vitest";
import { api } from "./api";
import { useRoomHistory } from "./useRoomHistory";
import { EventJournal } from "./EventJournal";
import type { HistoryEvent, HistoryPage } from "./types";
vi.mock("./api", () => ({ api: vi.fn() }));
const event = (sequence: string): HistoryEvent => ({
  id: `event-${sequence}`,
  sequence,
  type: "GM_MESSAGE",
  createdAt: "2026-09-17T00:00:00Z",
  payload: { text: `Событие ${sequence}` },
});
const page = (
  sequences: string[],
  nextCursor: string | null = null,
): HistoryPage => ({ events: sequences.map(event), nextCursor });
function deferred() {
  let resolve!: (value: HistoryPage) => void;
  const promise = new Promise<HistoryPage>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
beforeEach(() => {
  vi.mocked(api).mockReset();
});
afterEach(() => {
  vi.restoreAllMocks();
});

it("merges older history with every page missed offline without losing either", async () => {
  const older = deferred();
  vi.mocked(api).mockImplementation(async (path) => {
    if (path.endsWith("?before=2")) return older.promise;
    if (path.endsWith("?after=3")) return page(["4", "5"], "5");
    if (path.endsWith("?after=5")) return page(["6"]);
    return page(["2", "3"], "2");
  });
  const { result } = renderHook(() => useRoomHistory("room"));
  await waitFor(() => expect(result.current.initialized).toBe(true));
  let loading!: Promise<void>;
  act(() => {
    loading = result.current.loadOlder();
  });
  await act(async () => {
    await result.current.refresh();
  });
  await act(async () => {
    older.resolve(page(["1"]));
    await loading;
  });
  expect(result.current.events.map((e) => e.sequence)).toEqual([
    "1",
    "2",
    "3",
    "4",
    "5",
    "6",
  ]);
  expect(result.current.nextCursor).toBe(null);
});

it("rechecks events signalled while the initial page was loading", async () => {
  const first = deferred();
  vi.mocked(api)
    .mockReturnValueOnce(first.promise)
    .mockResolvedValueOnce(page(["2"]));
  const { result } = renderHook(() => useRoomHistory("room"));
  let refresh!: Promise<void>;
  act(() => {
    refresh = result.current.refresh();
  });
  await act(async () => {
    first.resolve(page(["1"]));
    await refresh;
  });
  expect(api).toHaveBeenLastCalledWith("/rooms/room/events/page?after=1");
  expect(result.current.events.map((e) => e.sequence)).toEqual(["1", "2"]);
});

it("ignores a late page from a room that was left", async () => {
  const old = deferred();
  vi.mocked(api)
    .mockReturnValueOnce(old.promise)
    .mockResolvedValueOnce(page(["20"]));
  const { result, rerender } = renderHook(({ id }) => useRoomHistory(id), {
    initialProps: { id: "one" },
  });
  rerender({ id: "two" });
  await waitFor(() => expect(result.current.events[0]?.sequence).toBe("20"));
  await act(async () => {
    old.resolve(page(["1"], "1"));
  });
  expect(result.current.roomId).toBe("two");
  expect(result.current.events.map((e) => e.sequence)).toEqual(["20"]);
});

it("keeps the same older cursor on failure and retries without rounding bigint sequences", async () => {
  vi.mocked(api)
    .mockResolvedValueOnce(page(["9007199254740994"], "9007199254740994"))
    .mockRejectedValueOnce(new Error("Нет связи"))
    .mockResolvedValueOnce(page(["9007199254740993"]));
  const { result } = renderHook(() => useRoomHistory("room"));
  await waitFor(() => expect(result.current.initialized).toBe(true));
  await act(async () => {
    await result.current.loadOlder();
  });
  expect(result.current.olderError).toBe("Нет связи");
  expect(result.current.nextCursor).toBe("9007199254740994");
  await act(async () => {
    await result.current.loadOlder();
  });
  expect(api).toHaveBeenLastCalledWith(
    "/rooms/room/events/page?before=9007199254740994",
  );
  expect(result.current.events.map((e) => e.sequence)).toEqual([
    "9007199254740993",
    "9007199254740994",
  ]);
  expect(result.current.olderError).toBe("");
});

it("recovers from an initial error and updates an initially empty journal", async () => {
  vi.mocked(api)
    .mockRejectedValueOnce(new Error("Недоступно"))
    .mockResolvedValueOnce(page([]))
    .mockResolvedValueOnce(page(["1"]));
  const { result } = renderHook(() => useRoomHistory("room"));
  await waitFor(() => expect(result.current.error).toBe("Недоступно"));
  await act(async () => {
    await result.current.refresh();
  });
  expect(result.current.initialized).toBe(true);
  await act(async () => {
    await result.current.refresh();
  });
  expect(api).toHaveBeenLastCalledWith("/rooms/room/events/page?after=0");
  expect(result.current.events).toHaveLength(1);
});

it("keeps the reading position when prepending or receiving new events", async () => {
  const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
  const scrollBy = vi.spyOn(window, "scrollBy").mockImplementation(() => {});
  let firstTop = 100;
  vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      return {
        top: this.dataset?.eventId === "event-2" ? firstTop : 3000,
      } as DOMRect;
    },
  );
  const history: ReturnType<typeof useRoomHistory> = {
    roomId: "room",
    events: [event("2"), event("3")],
    nextCursor: "2",
    initialized: true,
    loading: false,
    loadingOlder: false,
    error: "",
    olderError: "",
    refresh: vi.fn(async () => {}),
    loadOlder: vi.fn(async () => {}),
  };
  const view = render(<EventJournal history={history} />);
  expect(scroll).toHaveBeenCalledOnce();
  fireEvent.scroll(window);
  fireEvent.click(
    screen.getByRole("button", { name: "Загрузить ранние события" }),
  );
  firstTop = 500;
  view.rerender(
    <EventJournal
      history={{
        ...history,
        events: [event("1"), ...history.events],
        nextCursor: null,
      }}
    />,
  );
  expect(scrollBy).toHaveBeenCalledWith(0, 400);
  view.rerender(
    <EventJournal
      history={{
        ...history,
        events: [event("1"), ...history.events, event("4")],
        nextCursor: null,
      }}
    />,
  );
  expect(scroll).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByRole("button", { name: "К последним событиям" }));
  expect(scroll).toHaveBeenCalledTimes(2);
});

it("does not pull the page away from the composer while the player types", () => {
  const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
  const history: ReturnType<typeof useRoomHistory> = {
    roomId: "room", events: [event("1")], nextCursor: null,
    initialized: true, loading: false, loadingOlder: false,
    error: "", olderError: "", refresh: vi.fn(async () => {}),
    loadOlder: vi.fn(async () => {}),
  };
  const view = render(<><textarea aria-label="Действие" /><EventJournal history={history} /></>);
  expect(scroll).toHaveBeenCalledOnce();
  screen.getByRole("textbox", { name: "Действие" }).focus();
  view.rerender(<><textarea aria-label="Действие" /><EventJournal history={{...history, events: [event("1"), event("2")]}} /></>);
  expect(scroll).toHaveBeenCalledOnce();
  expect(screen.getByRole("button", { name: "К последним событиям" })).toBeTruthy();
});
