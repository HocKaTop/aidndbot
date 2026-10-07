import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import WebApp from "@twa-dev/sdk";
import {
  ArrowLeft,
  ArrowRight,
  BookOpen,
  Check,
  Copy,
  Flame,
  Plus,
  Scroll,
  Send,
  Settings as SettingsIcon,
  Shield,
  Skull,
  Sparkles,
  Swords,
  Users,
  Trash2,
} from "lucide-react";
import { api, setToken, ApiError } from "./api";
import { useRoomSocket } from "./useRoomSocket";
import { useRoomHistory } from "./useRoomHistory";
import { EventJournal } from "./EventJournal";
import { Toast } from "./Toast";
import { DeleteRoomDialog } from "./DeleteRoomDialog";
import { CampaignEnding, FinishCampaignDialog } from "./CampaignEnding";
import { SettingsForm, HeroCard } from "./components";
import type { Room, Settings, User } from "./types";
const defaults: Settings = {
  name: "",
  setting: "Тёмное фэнтези",
  worldDescription: "",
  tone: "Мрачный, таинственный",
  rules: "Упрощённая fantasy RPG",
  difficulty: "Обычная",
  gmStyle: "Атмосферный, с выбором для игроков",
  ollamaModel: "",
  maxPlayers: 6,
};
const quickAdventure: Settings = {
  ...defaults,
  name: "Последний фонарь",
  worldDescription:
    "Короткое приключение для новичков на одну сессию. Отряд прибывает в деревню Тихий Брод: единственный фонарь, защищающий мост от тумана, погас. До заката нужно найти пропавший огненный камень и вернуть свет. Начало — у моста, где смотрительница Мира просит помощи. Она видела следы к старой мельнице. План: разговор и осмотр моста, исследование мельницы с простой загадкой, встреча с напуганным похитителем, возвращение камня. Дай возможность договориться или вступить в короткий бой по выбору игроков. Раскрывай тайну постепенно, начни без боя. После возвращения камня заверши квест и расскажи эпилог.",
  tone: "Таинственный, уютный, с надеждой",
  gmStyle:
    "Короткие сцены, понятная цель и выбор для новичков. Не решай за игроков.",
};

function SkipTurnButton({
  since,
  turn,
  name,
  disabled,
  onSkip,
}: {
  since?: string;
  turn: number;
  name: string;
  disabled: boolean;
  onSkip: (turn: number) => void;
}) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const deadline = Date.parse(since ?? "");
  const wait = Number.isFinite(deadline)
    ? Math.max(0, Math.ceil((deadline + 60000 - now) / 1000))
    : 60;
  return (
    <button
      className="secondary"
      disabled={disabled || wait > 0}
      onClick={() => onSkip(turn)}
    >
      {wait > 0 ? `Ждём игрока · ${wait} с` : `Пропустить ход: ${name}`}
    </button>
  );
}
const actionExamples = [
  "Я осматриваюсь и ищу, что поможет нам достичь цели.",
  "Я внимательно прислушиваюсь: что происходит рядом?",
  "Я обсуждаю с отрядом, с чего нам лучше начать.",
];
const labels: Record<string, string> = {
  WAITING: "Сбор отряда",
  PLAYING: "Приключение",
  PAUSED: "На паузе",
  FINISHED: "Завершена",
};
const attackAbilities = {
  warrior: { id: "power_strike", label: "Мощный удар", cost: 1 },
  rogue: { id: "precise_strike", label: "Точный удар", cost: 1 },
  mage: { id: "firebolt", label: "Огненный снаряд", cost: 0 },
};
const defenseAbilities = {
  warrior: { id: "guard", label: "Стойка стража" },
  rogue: { id: "evade", label: "Уклонение" },
  mage: { id: "barrier", label: "Магический барьер" },
};
export default function App() {
  const [user, setUser] = useState<User | null>(null),
    [rooms, setRooms] = useState<Room[]>([]),
    [room, setRoom] = useState<Room | null>(null);
  const history = useRoomHistory(room?.id);
  const [screen, setScreen] = useState("home"),
    [tab, setTab] = useState("game"),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [loading, setLoading] = useState(true),
    [code, setCode] = useState(WebApp.initDataUnsafe.start_param || ""),
    [notice, setNotice] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<Room | null>(null);
  const [finishTarget, setFinishTarget] = useState<string | null>(null);
  const [hiddenDeliveryId, setHiddenDeliveryId] = useState<string | null>(null);
  const active = useRef<string | null>(null);
  const refreshSequence = useRef(0);
  const previousRoom = useRef<{ id: string; status: string } | null>(null);
  useEffect(() => {
    if (
      room?.id === previousRoom.current?.id &&
      previousRoom.current?.status === "WAITING" &&
      room?.status === "PLAYING"
    ) {
      setTab("game");
    }
    previousRoom.current = room ? { id: room.id, status: room.status } : null;
  }, [room?.id, room?.status]);
  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setNotice(""), 4500);
    return () => window.clearTimeout(timer);
  }, [notice]);
  useEffect(() => {
    if (!user || !error) return;
    const timer = window.setTimeout(() => setError(""), 8000);
    return () => window.clearTimeout(timer);
  }, [error, user]);
  const run = async (fn: () => Promise<void>) => {
    setError("");
    setBusy(true);
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Ошибка");
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    let live = true;
    const init = async () => {
      if (!WebApp.initData) {
        setLoading(false);
        return;
      }
      try {
        const data = await api<{ token: string; user: User }>(
          "/auth/telegram",
          "POST",
          { initData: WebApp.initData },
        );
        if (!live) return;
        setToken(data.token);
        setUser(data.user);
        const list = await api<Room[]>("/rooms");
        if (live) setRooms(list);
      } catch (e) {
        if (live) setError(String(e));
      } finally {
        if (live) setLoading(false);
      }
    };
    void init();
    return () => {
      live = false;
    };
  }, []);
  const removed = useCallback((message: string) => {
    active.current = null;
    refreshSequence.current++;
    setRoom(null);
    setDeleteTarget(null);
    setFinishTarget(null);
    setScreen("home");
    setError("");
    setNotice(message);
    void api<Room[]>("/rooms")
      .then(setRooms)
      .catch((e) => setError(e.message));
  }, []);
  const refresh = useCallback(() => {
    const rid = active.current;
    if (!rid) return;
    const sequence = ++refreshSequence.current;
    void Promise.all([api<Room>("/rooms/" + rid), history.refresh(rid)])
      .then(([r]) => {
        if (active.current === rid && sequence === refreshSequence.current) {
          setRoom(r);
        }
      })
      .catch((e) => {
        if (active.current !== rid || sequence !== refreshSequence.current)
          return;
        if (e instanceof ApiError && (e.status === 404 || e.status === 403))
          removed("Комната удалена или больше недоступна");
        else setError(e.message);
      });
  }, [removed, history.refresh]);
  const {
    status,
    processing,
    actor,
    send,
    draft: text,
    setDraft: setText,
    delivery,
    pending,
    retry,
  } = useRoomSocket(
    room?.id,
    refresh,
    setError,
    removed,
    user?.id,
    room?.state.turn,
  );
  useEffect(() => {
    if (delivery?.status !== "completed") return;
    const id = delivery.packet.id;
    const timer = window.setTimeout(() => setHiddenDeliveryId(id), 3500);
    return () => window.clearTimeout(timer);
  }, [delivery?.packet.id, delivery?.status]);
  const open = (r: Room) => {
    active.current = r.id;
    setRoom(r);
    setScreen("room");
    setTab(r.status === "WAITING" ? "party" : "game");
    refresh();
  };
  const home = () => {
    active.current = null;
    setRoom(null);
    setDeleteTarget(null);
    setFinishTarget(null);
    setScreen("home");
    void run(async () => setRooms(await api<Room[]>("/rooms")));
  };
  const owner = !!room && room.ownerId === user?.id;
  const hero = room?.state.characters.find((h) => h.userId === user?.id);
  const classAttack = hero?.classId ? attackAbilities[hero.classId] : undefined;
  const classDefense = hero?.classId
    ? defenseAbilities[hero.classId]
    : undefined;
  const npcPresent = (npc: Room["state"]["npcs"][number]) =>
    npc.locationId && room?.state.scene?.id
      ? npc.locationId === room.state.scene.id
      : npc.location === room?.state.scene?.location;
  const presentNPCs =
    room?.state.npcs.filter((npc) => npc.alive && npcPresent(npc)) ?? [];
  const localHostile = presentNPCs.some((npc) => npc.disposition === "hostile");
  const lantern =
    room?.state.settings.name === "Последний фонарь" &&
    room.state.settings.worldDescription.toLowerCase().includes("тихий брод") &&
    room.state.settings.worldDescription
      .toLowerCase()
      .includes("огненный камень");
  const place =
    `${room?.state.scene?.title ?? ""} ${room?.state.scene?.location ?? ""}`.toLowerCase();
  const myStone = hero?.inventory.find(
    (item) =>
      item.quantity > 0 && item.name.toLowerCase() === "огненный камень",
  );
  const partyHasStone = room?.state.characters.some((character) =>
    character.inventory.some(
      (item) =>
        item.quantity > 0 && item.name.toLowerCase() === "огненный камень",
    ),
  );
  const thiefReleasedStone = room?.state.npcs.some(
    (npc) =>
      npc.name.toLowerCase().includes("похит") &&
      npcPresent(npc) &&
      (!npc.alive || npc.disposition === "friendly"),
  );
  const waiting = room?.status === "WAITING";
  const combatHero = room?.state.combat
    ? room.state.characters.find(
        (h) =>
          h.userId === room.state.combatOrder?.[room.state.combatIndex ?? 0],
      )
    : undefined;
  const myTurn = !combatHero || combatHero.userId === user?.id;
  const pendingMembers =
    room?.members.filter(
      (m) =>
        !m.ready ||
        !room.state.characters.some((h) => h.userId === m.userId && h.hp > 0),
    ) ?? [];
  const applyRoom = (next: Room) => {
    if (active.current === next.id) setRoom(next);
  };
  const createHero = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const data = new FormData(e.currentTarget);
    void run(async () => {
      applyRoom(
        await api<Room>(
          `/rooms/${room!.id}/characters`,
          "POST",
          Object.fromEntries(data),
        ),
      );
    });
  };
  const action = (e: FormEvent) => {
    e.preventDefault();
    if (!myTurn || processing || pending || room?.status !== "PLAYING") return;
    if (text.trim()) send("player_action", { text });
  };
  const visibleDelivery =
    screen === "room" &&
    room &&
    delivery &&
    (delivery.status !== "completed" || delivery.packet.id !== hiddenDeliveryId)
      ? delivery
      : null;
  const retryAvailable =
    visibleDelivery &&
    ["failed", "unknown"].includes(visibleDelivery.status) &&
    (visibleDelivery.packet.type !== "player_action" ||
      visibleDelivery.packet.data.text === text);
  if (loading)
    return (
      <main className="gate">
        <Flame size={42} />
        <h1>Nocturna</h1>
        <p>Открываем книгу приключений…</p>
      </main>
    );
  if (!user)
    return (
      <main className="gate">
        <div className="sigil">
          <Swords size={42} />
        </div>
        <div className="eyebrow">Мультиплеерная fantasy RPG</div>
        <h1>
          История начинается
          <br />
          <em>с твоего выбора.</em>
        </h1>
        <p>
          Создай отряд, исследуй мир и доверь повествование AI-ведущему. Открой
          Nocturna через кнопку в Telegram-боте.
        </p>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <div className="pill">
          <Shield size={15} /> Вход через Telegram
        </div>
      </main>
    );
  return (
    <div className="shell">
      <header>
        <button className="brand" onClick={home}>
          <Flame size={23} />
          <span>NOCTURNA</span>
        </button>
        <div className="avatar" title={user.firstName}>
          {user.firstName.slice(0, 1)}
        </div>
      </header>
      {(error ||
        notice ||
        visibleDelivery ||
        (screen === "room" && processing && actor)) && (
        <div className="toast-viewport" role="region" aria-label="Оповещения">
          {error && (
            <Toast tone="error" title={error} onClose={() => setError("")} />
          )}
          {notice && (
            <Toast
              tone="success"
              title={notice}
              onClose={() => setNotice("")}
            />
          )}
          {visibleDelivery ? (
            <Toast
              tone={
                ["failed", "unknown"].includes(visibleDelivery.status)
                  ? "error"
                  : visibleDelivery.status === "completed"
                    ? "success"
                    : "info"
              }
              title={
                {
                  sent: "Отправлено — ждём подтверждения",
                  queued: "Принято сервером — ждём выполнения",
                  processing: "Действие выполняется",
                  unknown: "Результат пока не подтверждён",
                  failed: "Действие не выполнено",
                  completed: "Действие выполнено",
                }[visibleDelivery.status]
              }
              action={
                retryAvailable && (
                  <button
                    className="toast-retry"
                    disabled={status !== "online" || processing}
                    onClick={retry}
                  >
                    Повторить отправку
                  </button>
                )
              }
            >
              {visibleDelivery.message && <p>{visibleDelivery.message}</p>}
              {visibleDelivery.status === "unknown" && (
                <p>
                  Текст сохранён. После подключения проверим результат.
                  Повторная отправка не создаст второй ход.
                </p>
              )}
            </Toast>
          ) : (
            screen === "room" &&
            processing &&
            actor && (
              <Toast
                tone="info"
                title={`Мастер обрабатывает действие: ${actor}`}
              />
            )
          )}
        </div>
      )}
      <main>
        {screen === "home" && (
          <>
            <section className="welcome">
              <div className="eyebrow">
                <span className="dot" /> Твоя следующая история
              </div>
              <h1>
                Собери отряд.
                <br />
                <em>Измени судьбу.</em>
              </h1>
              <p>
                Задай свой мир — ведущий придумает историю,
                <br />а ты играй один или с друзьями.
              </p>
              <button
                className="primary"
                disabled={busy}
                onClick={() => setScreen("create")}
              >
                <Plus size={18} />
                Новая кампания
              </button>
              <div className="welcome-art" aria-hidden="true">
                <Swords />
                <span>XX</span>
              </div>
            </section>
            <section className="panel stack">
              <div className="eyebrow">
                Первое приключение · можно одному или с друзьями
              </div>
              <h2>Последний фонарь</h2>
              <p>
                Над деревней сгущается туман. Найдите пропавший огненный камень
                и верните свет до заката. Мир и завязка уже подготовлены —
                осталось собрать отряд.
              </p>
              <button
                className="secondary"
                disabled={busy}
                onClick={() =>
                  void run(async () => {
                    open(await api<Room>("/rooms", "POST", quickAdventure));
                  })
                }
              >
                <Sparkles size={18} /> Быстрое приключение
              </button>
            </section>
            <form
              className="join panel"
              onSubmit={(e) => {
                e.preventDefault();
                void run(async () =>
                  open(await api<Room>("/rooms/join", "POST", { code })),
                );
              }}
            >
              <label htmlFor="code">Тебя уже ждут?</label>
              <div className="inline">
                <input
                  id="code"
                  placeholder="Код приглашения"
                  maxLength={32}
                  required
                  value={code}
                  onChange={(e) => setCode(e.target.value.toUpperCase())}
                />
                <button disabled={busy} aria-label="Присоединиться">
                  <ArrowRight size={20} />
                </button>
              </div>
            </form>
            <div className="section-title">
              <h2>Твои кампании</h2>
              <span>{rooms.length.toString().padStart(2, "0")}</span>
            </div>
            {rooms.length === 0 ? (
              <section className="empty">
                <BookOpen size={30} />
                <h3>Первая глава ещё не написана</h3>
                <p>Создай кампанию или присоединись к друзьям по коду.</p>
              </section>
            ) : (
              <div className="stack">
                {rooms.map((r) => (
                  <button
                    className="room-card"
                    key={r.id}
                    onClick={() => open(r)}
                  >
                    <div className="room-icon">
                      <Swords size={25} />
                    </div>
                    <div>
                      <span className="eyebrow">
                        {r.state.settings.setting}
                      </span>
                      <h3>{r.state.settings.name}</h3>
                      <p>
                        <Users size={13} />
                        {r.members.length} / {r.state.settings.maxPlayers}
                        <span>·</span>
                        {labels[r.status]}
                      </p>
                    </div>
                    <ArrowRight size={18} />
                  </button>
                ))}
              </div>
            )}
            <footer>Каждый бросок — новая возможность.</footer>
          </>
        )}
        {(screen === "create" || screen === "settings") && (
          <>
            <button
              className="back"
              onClick={() => setScreen(room ? "room" : "home")}
            >
              <ArrowLeft size={17} />
              Назад
            </button>
            <div className="eyebrow">Подготовка приключения</div>
            <h1>{screen === "create" ? "Новая кампания" : "Настройки мира"}</h1>
            <section className="panel">
              <SettingsForm
                initial={
                  screen === "settings" ? room!.state.settings : defaults
                }
                busy={busy}
                onSave={(s) =>
                  void run(async () => {
                    const r = await api<Room>(
                      screen === "settings" ? "/rooms/" + room!.id : "/rooms",
                      screen === "settings" ? "PATCH" : "POST",
                      s,
                    );
                    open(r);
                  })
                }
              />
            </section>
          </>
        )}
        {screen === "room" && room && (
          <>
            <button className="back" onClick={home}>
              <ArrowLeft size={17} />
              Все кампании
            </button>
            <div className="room-heading">
              <div>
                <div className="eyebrow">{room.state.settings.setting}</div>
                <h1>{room.state.settings.name}</h1>
              </div>
              {owner && (
                <button
                  aria-label="Удалить комнату"
                  title="Удалить комнату"
                  onClick={() => setDeleteTarget(room)}
                >
                  <Trash2 size={20} />
                </button>
              )}
              {owner && waiting && (
                <button
                  aria-label="Настройки комнаты"
                  onClick={() => setScreen("settings")}
                >
                  <SettingsIcon size={20} />
                </button>
              )}
            </div>
            <div className="room-meta">
              <span className={"dot " + (status === "online" ? "" : "muted")} />
              {status === "online" ? "На связи" : "Подключение…"}
              <span>·</span>
              {labels[room.status]}
              {room.state.combat && <span className="combat-label">Бой</span>}
            </div>
            {owner && ["PLAYING", "PAUSED"].includes(room.status) && (
              <button
                className="secondary"
                disabled={busy || processing || pending || status !== "online"}
                onClick={() => setFinishTarget(room.id)}
              >
                Завершить приключение
              </button>
            )}
            {["PLAYING", "PAUSED"].includes(room.status) &&
              !room.state.combat &&
              room.state.quests.length > 0 &&
              room.state.quests.every((q) => q.status !== "ACTIVE") && (
                <section className="panel stack" aria-label="Цели приключения">
                  <h2>Активных целей больше нет</h2>
                  <p>
                    Выполнено квестов:{" "}
                    {
                      room.state.quests.filter((q) => q.status === "COMPLETED")
                        .length
                    }
                    . Провалено:{" "}
                    {
                      room.state.quests.filter((q) => q.status === "FAILED")
                        .length
                    }
                    .
                  </p>
                  <p>
                    {owner
                      ? "Если ваша история закончена, подведи итоги и сохрани финал. Можно также продолжить игру и найти новую цель."
                      : "Обсудите финал с отрядом. Владелец может завершить приключение и сохранить итоги."}
                  </p>
                  {owner && (
                    <button
                      className="primary"
                      disabled={
                        busy || processing || pending || status !== "online"
                      }
                      onClick={() => setFinishTarget(room.id)}
                    >
                      Подвести итоги
                    </button>
                  )}
                </section>
              )}
            {waiting && (
              <section className="panel stack" aria-label="Подготовка отряда">
                <h2>
                  {processing
                    ? "Мастер готовит вступление…"
                    : "До первого приключения"}
                </h2>
                <p>
                  Создай героя, пригласи друзей по коду и нажми «Я готов к
                  приключению». Можно играть одному. Когда все готовы, владелец
                  начинает игру — мастер создаст сцену и первую цель.
                </p>
                {pendingMembers.length > 0 ? (
                  <p role="status">
                    Ждём:{" "}
                    {pendingMembers
                      .map((m) => {
                        const h = room.state.characters.find(
                          (c) => c.userId === m.userId,
                        );
                        return `${m.name} — ${!h ? "создать героя" : h.hp <= 0 ? "нужен живой герой" : "подтвердить готовность"}`;
                      })
                      .join("; ")}
                  </p>
                ) : (
                  <p role="status">
                    Все готовы.{" "}
                    {owner ? "Можно начинать!" : "Ждём старта от владельца."}
                  </p>
                )}
                {tab !== "party" && (
                  <button className="secondary" onClick={() => setTab("party")}>
                    Открыть отряд
                  </button>
                )}
              </section>
            )}
            {tab === "party" && (
              <>
                <section className="panel">
                  <div className="section-title">
                    <h2>Отряд</h2>
                    <span>
                      {room.members.length}/{room.state.settings.maxPlayers}
                    </span>
                  </div>
                  {room.members.map((m) => {
                    const h = room.state.characters.find(
                      (c) => c.userId === m.userId,
                    );
                    return (
                      <div className="member" key={m.userId}>
                        <div className="avatar">{m.name.slice(0, 1)}</div>
                        <div>
                          <strong>
                            {m.name}
                            {m.userId === user.id ? " · ты" : ""}
                          </strong>
                          <small>
                            {h
                              ? `${h.name} · ${h.class}`
                              : "Персонаж ещё не создан"}
                            {m.role === "OWNER" ? " · Ведущий отряда" : ""}
                          </small>
                        </div>
                        {m.ready ? (
                          <Check size={18} className="green" />
                        ) : (
                          <span className="pill">
                            {waiting
                              ? h
                                ? "Ждём готовности"
                                : "Нет героя"
                              : "В отряде"}
                          </span>
                        )}
                      </div>
                    );
                  })}
                  {waiting && (
                    <button
                      className="secondary full"
                      onClick={() =>
                        void run(async () => {
                          await navigator.clipboard.writeText(
                            room.inviteUrl || room.code,
                          );
                          setNotice("Приглашение скопировано");
                        })
                      }
                    >
                      <Copy size={16} />
                      Пригласить друзей · {room.code}
                    </button>
                  )}
                  <p>
                    Чтобы получать события в Telegram, напиши боту{" "}
                    <code>/room {room.code}</code>. Уведомления можно выключить
                    командой <code>/mute</code>.
                  </p>
                </section>
                <section className="panel">
                  <div className="eyebrow">Этот мир</div>
                  <h3>{room.state.settings.setting}</h3>
                  <p>
                    {room.state.settings.worldDescription ||
                      "Детали мира откроются с первым действием."}
                  </p>
                  <div className="pill">{room.state.settings.tone}</div>
                </section>
                {waiting && !hero && (
                  <section className="panel">
                    <h2>Твой персонаж</h2>
                    <form className="stack" onSubmit={createHero}>
                      <label>
                        Имя
                        <input
                          name="name"
                          required
                          maxLength={40}
                          placeholder="Как тебя зовут?"
                        />
                      </label>
                      <div className="two">
                        <label>
                          Раса
                          <select name="race">
                            <option>Человек</option>
                            <option>Эльф</option>
                            <option>Дварф</option>
                            <option>Полурослик</option>
                          </select>
                        </label>
                        <label>
                          Класс
                          <select name="class">
                            <option>Воин</option>
                            <option>Плут</option>
                            <option>Маг</option>
                          </select>
                        </label>
                      </div>
                      <small>
                        Воин: 24 HP, броня 15 и мощный удар. Плут: 18 HP, точный
                        удар и уклонение. Маг: 16 HP, огненный снаряд и барьер.
                        Расы пока описательные.
                      </small>
                      <button className="primary" disabled={busy}>
                        Создать персонажа
                      </button>
                    </form>
                  </section>
                )}
                {waiting && hero && (
                  <button
                    className="secondary full"
                    disabled={processing || pending || status !== "online"}
                    onClick={() =>
                      send("ready", {
                        ready: !room.members.find((m) => m.userId === user.id)
                          ?.ready,
                      })
                    }
                  >
                    <Check size={17} />
                    {room.members.find((m) => m.userId === user.id)?.ready
                      ? "Я пока не готов"
                      : "Я готов к приключению"}
                  </button>
                )}
                {owner && room.status !== "FINISHED" && (
                  <button
                    className="primary full"
                    disabled={
                      busy ||
                      processing ||
                      pending ||
                      status !== "online" ||
                      !room.state.characters.length ||
                      (waiting && pendingMembers.length > 0)
                    }
                    onClick={() => {
                      if (room.status !== "PLAYING") {
                        if (send("start_game", {})) setTab("game");
                        return;
                      }
                      void run(async () => {
                        applyRoom(
                          await api<Room>(
                            `/rooms/${room.id}/${room.status === "PLAYING" ? "pause" : "start"}`,
                            "POST",
                          ),
                        );
                        setTab("game");
                      });
                    }}
                  >
                    {processing && waiting
                      ? "Готовим вступление…"
                      : room.status === "PLAYING"
                        ? "Поставить на паузу"
                        : room.status === "PAUSED"
                          ? "Продолжить кампанию"
                          : "Начать приключение"}
                    <ArrowRight size={18} />
                  </button>
                )}
                {!owner && waiting && (
                  <button
                    className="back"
                    onClick={() =>
                      void run(async () => {
                        await api(`/rooms/${room.id}/leave`, "POST");
                        home();
                      })
                    }
                  >
                    Покинуть комнату
                  </button>
                )}
              </>
            )}
            {room.status === "FINISHED" && (
              <CampaignEnding room={room} onHome={home} />
            )}
            {hero && hero.hp <= 0 && room.status !== "FINISHED" && (
              <p className="error">
                Твой персонаж пал и больше не может действовать. Ты можешь
                следить за приключением отряда.
              </p>
            )}
            {tab === "game" && (
              <>
                {room.state.scene ? (
                  <section className="scene">
                    <div className="eyebrow">
                      <Scroll size={14} />
                      Текущая сцена
                    </div>
                    <h2>{room.state.scene.title}</h2>
                    <p>{room.state.scene.description}</p>
                    {room.state.quests
                      .filter((q) => q.status === "ACTIVE")
                      .map((q) => (
                        <div key={q.id}>
                          <div className="eyebrow">Цель приключения</div>
                          <h3>{q.title}</h3>
                          <p>{q.description}</p>
                        </div>
                      ))}
                    {owner &&
                      !lantern &&
                      room.status === "PLAYING" &&
                      room.state.quests.some(
                        (q) =>
                          q.status === "COMPLETED" || q.status === "FAILED",
                      ) && (
                        <details>
                          <summary>Исправить итог цели</summary>
                          {room.state.quests
                            .filter(
                              (q) =>
                                q.status === "COMPLETED" ||
                                q.status === "FAILED",
                            )
                            .map((q) => (
                              <button
                                key={q.id}
                                type="button"
                                className="secondary"
                                disabled={
                                  busy ||
                                  processing ||
                                  pending ||
                                  status !== "online"
                                }
                                onClick={() =>
                                  send("reopen_quest", { target: q.id })
                                }
                              >
                                Вернуть цель «{q.title}» в игру
                              </button>
                            ))}
                        </details>
                      )}
                  </section>
                ) : (
                  <section className="empty">
                    <Sparkles size={28} />
                    <h3>
                      {waiting ? "Отряд собирается" : "Мир ждёт первого шага"}
                    </h3>
                    <p>
                      {waiting
                        ? "Создайте персонажей во вкладке «Отряд»."
                        : "Мастер готовит место встречи и первую цель отряда."}
                    </p>
                  </section>
                )}
                {room.status === "PLAYING" &&
                  room.state.pendingQuestCompletion && (
                    <section
                      className="panel stack"
                      aria-label="Предложение завершить цель"
                    >
                      <div className="eyebrow">Решение об итоге</div>
                      <h2>
                        {room.state.pendingQuestCompletion.status === "FAILED"
                          ? "Цель провалена?"
                          : "Цель достигнута?"}
                      </h2>
                      <p>{room.state.pendingQuestCompletion.reason}</p>
                      <p>
                        Ведущий предлагает исход цели. Пока она активна, история
                        продолжается.
                      </p>
                      {owner ? (
                        <div className="combat-actions">
                          <button
                            className="primary"
                            disabled={
                              processing || pending || status !== "online"
                            }
                            onClick={() =>
                              send("confirm_quest", {
                                proposalId:
                                  room.state.pendingQuestCompletion!.id,
                              })
                            }
                          >
                            Подтвердить
                            {room.state.pendingQuestCompletion.status !==
                              "FAILED" &&
                            room.state.quests.filter(
                              (q) => q.status === "ACTIVE",
                            ).length === 1 &&
                            room.state.quests.every(
                              (q) => q.status !== "FAILED",
                            )
                              ? " и завершить"
                              : ""}
                          </button>
                          <button
                            className="secondary"
                            disabled={
                              processing || pending || status !== "online"
                            }
                            onClick={() =>
                              send("continue_quest", {
                                proposalId:
                                  room.state.pendingQuestCompletion!.id,
                              })
                            }
                          >
                            Продолжить историю
                          </button>
                        </div>
                      ) : (
                        <p>
                          Владелец кампании может подтвердить итог или
                          продолжить историю.
                        </p>
                      )}
                    </section>
                  )}
                {lantern &&
                  room.status === "PLAYING" &&
                  hero &&
                  hero.hp > 0 &&
                  !room.state.combat &&
                  place.includes("мельн") &&
                  thiefReleasedStone &&
                  !partyHasStone && (
                    <button
                      className="primary full"
                      disabled={
                        processing || pending || !myTurn || status !== "online"
                      }
                      onClick={() => send("claim_stone", {})}
                    >
                      Забрать огненный камень
                    </button>
                  )}
                {lantern &&
                  room.status === "PLAYING" &&
                  hero &&
                  hero.hp > 0 &&
                  myStone &&
                  !room.state.combat &&
                  !localHostile &&
                  !place.includes("мост") &&
                  !place.includes("фонар") && (
                    <button
                      className="primary full"
                      disabled={
                        processing || pending || !myTurn || status !== "online"
                      }
                      onClick={() => send("return_to_bridge", {})}
                    >
                      Вернуться с камнем к мосту
                    </button>
                  )}
                {lantern &&
                  room.status === "PLAYING" &&
                  hero &&
                  hero.hp > 0 &&
                  myStone &&
                  !room.state.combat &&
                  !localHostile &&
                  (place.includes("мост") || place.includes("фонар")) && (
                    <button
                      className="primary full"
                      disabled={
                        processing || pending || !myTurn || status !== "online"
                      }
                      onClick={() => send("install_stone", {})}
                    >
                      Установить камень и завершить приключение
                    </button>
                  )}
                <EventJournal key={room.id} history={history} />
                {combatHero && (
                  <section className="panel stack" aria-label="Очередь боя">
                    <h2>
                      Раунд {room.state.combatRound} · Ходит {combatHero.name}
                    </h2>
                    <p>
                      {room.state.combatOrder
                        ?.map(
                          (id) =>
                            room.state.characters.find(
                              (h) => h.userId === id && h.hp > 0,
                            )?.name,
                        )
                        .filter(Boolean)
                        .join(" → ")}
                    </p>
                    <p>
                      После каждого хода отвечает один противник; несколько
                      врагов чередуются. При обрыве связи очередь сохраняется.
                      Владелец может пропустить ход после минуты ожидания.
                    </p>
                    {myTurn && (
                      <div className="combat-actions">
                        {classDefense && hero && (
                          <button
                            className="secondary"
                            disabled={
                              processing ||
                              pending ||
                              status !== "online" ||
                              room.status !== "PLAYING" ||
                              (hero.resource ?? 0) < 1
                            }
                            onClick={() =>
                              send("class_ability", {
                                ability: classDefense.id,
                              })
                            }
                          >
                            {classDefense.label} · +4 AC · 1 ресурс
                          </button>
                        )}
                        <button
                          className="secondary"
                          disabled={
                            processing ||
                            pending ||
                            status !== "online" ||
                            room.status !== "PLAYING"
                          }
                          onClick={() => send("defend_turn", {})}
                        >
                          Защищаться · +2 AC
                        </button>
                        <button
                          className="secondary"
                          disabled={
                            processing ||
                            pending ||
                            status !== "online" ||
                            room.status !== "PLAYING"
                          }
                          onClick={() => send("pass_turn", {})}
                        >
                          Пропустить мой ход
                        </button>
                        {room.state.scene?.exits?.map((id) => (
                          <button
                            key={id}
                            className="secondary"
                            disabled={
                              processing ||
                              pending ||
                              status !== "online" ||
                              room.status !== "PLAYING"
                            }
                            onClick={() => send("retreat", { target: id })}
                          >
                            Отступить:{" "}
                            {room.state.locations?.find(
                              (place) => place.id === id,
                            )?.title ?? "известный выход"}
                          </button>
                        ))}
                      </div>
                    )}
                    {owner && !myTurn && (
                      <SkipTurnButton
                        since={room.state.combatTurnSince}
                        turn={room.state.turn}
                        name={combatHero.name}
                        disabled={
                          processing ||
                          pending ||
                          status !== "online" ||
                          room.status !== "PLAYING"
                        }
                        onSkip={(turn) => send("skip_turn", { turn })}
                      />
                    )}
                  </section>
                )}
                {room.status === "PLAYING" &&
                  hero &&
                  hero.hp > 0 &&
                  presentNPCs.length > 0 && (
                    <section
                      className="panel stack"
                      aria-label="Боевые действия"
                    >
                      <h2>Персонажи рядом</h2>
                      {presentNPCs.map((npc) => (
                        <div className="combat-target" key={npc.id}>
                          <strong>
                            {npc.name} · {npc.hp}/{npc.maxHp} HP
                          </strong>
                          <div className="combat-actions">
                            <button
                              className="secondary"
                              disabled={
                                processing ||
                                pending ||
                                !myTurn ||
                                status !== "online"
                              }
                              onClick={() =>
                                send("attack_npc", { target: npc.id })
                              }
                            >
                              Атаковать {npc.name}
                            </button>
                            {classAttack && (
                              <button
                                className="secondary"
                                disabled={
                                  processing ||
                                  pending ||
                                  !myTurn ||
                                  status !== "online" ||
                                  (room.state.combat &&
                                    (hero.resource ?? 0) < classAttack.cost)
                                }
                                onClick={() =>
                                  send("class_ability", {
                                    ability: classAttack.id,
                                    target: npc.id,
                                  })
                                }
                              >
                                {classAttack.label}
                                {classAttack.cost > 0 ? " · 1 ресурс" : ""}
                              </button>
                            )}
                          </div>
                        </div>
                      ))}
                      {room.state.combat &&
                        hero.hp < hero.maxHp &&
                        hero.inventory
                          .filter(
                            (item) =>
                              item.type === "HEALING" && item.quantity > 0,
                          )
                          .slice(0, 1)
                          .map((item) => (
                            <button
                              key={item.id}
                              className="secondary"
                              disabled={
                                processing ||
                                pending ||
                                !myTurn ||
                                status !== "online"
                              }
                              onClick={() =>
                                send("use_item", { itemId: item.id })
                              }
                            >
                              Выпить зелье · {item.quantity} шт.
                            </button>
                          ))}
                    </section>
                  )}
                {room.status === "PLAYING" &&
                  hero &&
                  hero.hp > 0 &&
                  hero.inventory.some(
                    (item) => item.type === "HEALING" && item.quantity > 0,
                  ) &&
                  room.state.characters.some(
                    (ally) => ally.id !== hero.id && ally.hp <= 0,
                  ) && (
                    <section
                      className="panel stack"
                      aria-label="Помощь союзнику"
                    >
                      <h2>Помочь союзнику</h2>
                      {room.state.characters
                        .filter((ally) => ally.id !== hero.id && ally.hp <= 0)
                        .map((ally) => (
                          <button
                            key={ally.id}
                            className="secondary"
                            disabled={
                              processing ||
                              pending ||
                              !myTurn ||
                              status !== "online"
                            }
                            onClick={() =>
                              send("aid_ally", { target: ally.id })
                            }
                          >
                            Передать зелье: {ally.name}
                          </button>
                        ))}
                    </section>
                  )}
                {room.status === "PLAYING" &&
                  hero &&
                  hero.hp > 0 &&
                  hero.hp < hero.maxHp &&
                  !room.state.combat &&
                  !localHostile &&
                  room.state.scene?.id &&
                  hero.lastRestLocationId !== room.state.scene.id &&
                  !hero.restedLocationIds?.includes(room.state.scene.id) && (
                    <button
                      className="secondary full"
                      disabled={processing || pending || status !== "online"}
                      onClick={() => send("short_rest", {})}
                    >
                      Отдохнуть здесь · восстановить здоровье
                    </button>
                  )}
                {room.status !== "FINISHED" && (
                  <form className="composer" onSubmit={action}>
                    {room.state.scene && (
                      <div className="composer-context">
                        <strong>Сейчас: {room.state.scene.title}</strong>
                        {room.state.quests
                          .filter((q) => q.status === "ACTIVE")
                          .map((q) => (
                            <span key={q.id}>Цель: {q.title}</span>
                          ))}
                        {room.state.scene.facts?.at(-1) && (
                          <span>
                            Известно здесь: {room.state.scene.facts.at(-1)}
                          </span>
                        )}
                      </div>
                    )}
                    {room.status === "PLAYING" &&
                      room.state.turn === 0 &&
                      hero &&
                      hero.hp > 0 && (
                        <div className="action-examples">
                          <p>
                            Что попробовать? Выбери пример и дополни его или
                            напиши своё действие.
                          </p>
                          {actionExamples.map((example) => (
                            <button
                              type="button"
                              className="secondary"
                              key={example}
                              disabled={processing || pending}
                              onClick={() => setText(example)}
                            >
                              {example}
                            </button>
                          ))}
                        </div>
                      )}
                    <textarea
                      aria-label="Действие персонажа"
                      placeholder="Что ты делаешь?"
                      maxLength={1000}
                      value={text}
                      onChange={(e) => setText(e.target.value)}
                      disabled={
                        pending ||
                        waiting ||
                        room.status !== "PLAYING" ||
                        !myTurn ||
                        !hero ||
                        hero.hp <= 0
                      }
                    />
                    <div className="inline">
                      <button
                        className="primary"
                        aria-label="Отправить действие"
                        disabled={
                          processing ||
                          pending ||
                          !myTurn ||
                          status !== "online" ||
                          room.status !== "PLAYING" ||
                          !hero ||
                          hero.hp <= 0 ||
                          !text.trim()
                        }
                      >
                        <Send size={18} />
                      </button>
                    </div>
                  </form>
                )}
              </>
            )}
            {tab === "hero" &&
              (hero ? (
                <>
                  <HeroCard hero={hero} />
                  <section className="panel">
                    <div className="eyebrow">Снаряжение и находки</div>
                    <h2>Инвентарь</h2>
                    {hero.inventory.map((it) => (
                      <div className="inventory" key={it.id}>
                        <div>
                          <strong>
                            {it.name} <span>×{it.quantity}</span>
                          </strong>
                          <p>{it.description}</p>
                        </div>
                        {it.type === "HEALING" && (
                          <button
                            disabled={
                              processing ||
                              pending ||
                              !myTurn ||
                              status !== "online" ||
                              room.status !== "PLAYING" ||
                              hero.hp <= 0 ||
                              hero.hp >= hero.maxHp
                            }
                            onClick={() => send("use_item", { itemId: it.id })}
                          >
                            Выпить
                          </button>
                        )}
                      </div>
                    ))}
                    {!hero.inventory.length && <p>Пока пусто.</p>}
                  </section>
                </>
              ) : (
                <section className="empty">
                  <Shield size={30} />
                  <h3>Герой ещё не создан</h3>
                  <button onClick={() => setTab("party")}>
                    Перейти к отряду
                  </button>
                </section>
              ))}
            {tab === "quests" && (
              <>
                <section className="panel">
                  <div className="eyebrow">Зацепки вашей истории</div>
                  <h2>Квесты</h2>
                  {room.state.quests.length ? (
                    room.state.quests.map((q) => (
                      <article className="quest" key={q.id}>
                        <span className="pill">
                          {q.status === "ACTIVE"
                            ? "В процессе"
                            : q.status === "COMPLETED"
                              ? "Выполнен"
                              : "Провален"}
                        </span>
                        <h3>{q.title}</h3>
                        <p>{q.description}</p>
                      </article>
                    ))
                  ) : (
                    <p>Ведущий добавит задания по ходу приключения.</p>
                  )}
                </section>
                <section className="panel">
                  <h2>Персонажи мира</h2>
                  {room.state.npcs.length ? (
                    room.state.npcs.map((n) => (
                      <div className="npc" key={n.id}>
                        <div className="inline">
                          <strong>{n.name}</strong>
                          {!n.alive ? (
                            <Skull size={16} />
                          ) : (
                            <span className="pill">
                              {n.hp}/{n.maxHp} HP
                            </span>
                          )}
                        </div>
                        <p>{n.description}</p>
                        <small>
                          {n.disposition === "hostile"
                            ? "Враждебный"
                            : n.disposition === "friendly"
                              ? "Дружественный"
                              : "Нейтральный"}
                        </small>
                        {room.status === "PLAYING" &&
                          n.alive &&
                          npcPresent(n) &&
                          hero &&
                          hero.hp > 0 && (
                            <button
                              type="button"
                              className="secondary"
                              disabled={processing || pending || !myTurn}
                              onClick={() =>
                                send("attack_npc", { target: n.id })
                              }
                            >
                              <Swords size={16} /> Атаковать
                            </button>
                          )}
                      </div>
                    ))
                  ) : (
                    <p>Вы ещё ни с кем не встретились.</p>
                  )}
                </section>
              </>
            )}
          </>
        )}
      </main>
      {room && screen === "room" && (
        <nav aria-label="Разделы кампании">
          {[
            { key: "game", label: "Игра", Icon: BookOpen },
            { key: "hero", label: "Герой", Icon: Shield },
            { key: "party", label: "Отряд", Icon: Users },
            { key: "quests", label: "Журнал", Icon: Scroll },
          ].map(({ key, label, Icon }) => (
            <button
              key={key}
              className={tab === key ? "selected" : ""}
              onClick={() => setTab(key)}
            >
              <Icon size={21} />
              <span>{label}</span>
            </button>
          ))}
        </nav>
      )}
      {room &&
        screen === "room" &&
        finishTarget === room.id &&
        ["PLAYING", "PAUSED"].includes(room.status) && (
          <FinishCampaignDialog
            room={room}
            key={room.id}
            disabled={busy || processing || pending || status !== "online"}
            onCancel={() => setFinishTarget(null)}
            onConfirm={(code, text) => send("finish_game", { code, text })}
          />
        )}
      {deleteTarget && (
        <DeleteRoomDialog
          room={deleteTarget}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={async (code) => {
            const rid = deleteTarget.id;
            try {
              await api(`/rooms/${rid}`, "DELETE", { code });
            } catch (e) {
              if (!(e instanceof ApiError && e.status === 404)) throw e;
            }
            if (active.current === rid) removed("Комната удалена");
            setDeleteTarget(null);
          }}
        />
      )}
    </div>
  );
}
