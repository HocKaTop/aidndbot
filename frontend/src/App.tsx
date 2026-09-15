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
  Dices,
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
  X,
} from "lucide-react";
import { api, setToken, ApiError } from "./api";
import { useRoomSocket } from "./useRoomSocket";
import { DeleteRoomDialog } from "./DeleteRoomDialog";
import { SettingsForm, HeroCard } from "./components";
import type { GameEvent, Room, Settings, User } from "./types";
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
export default function App() {
  const [user, setUser] = useState<User | null>(null),
    [rooms, setRooms] = useState<Room[]>([]),
    [room, setRoom] = useState<Room | null>(null),
    [events, setEvents] = useState<GameEvent[]>([]);
  const [screen, setScreen] = useState("home"),
    [tab, setTab] = useState("game"),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [loading, setLoading] = useState(true),
    [code, setCode] = useState(WebApp.initDataUnsafe.start_param || ""),
    [text, setText] = useState(""),
    [dice, setDice] = useState("d20"),
    [notice, setNotice] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<Room | null>(null);
  const active = useRef<string | null>(null);
  const refreshSequence = useRef(0);
  const bottom = useRef<HTMLDivElement>(null);
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
    setEvents([]);
    setDeleteTarget(null);
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
    void Promise.all([
      api<Room>("/rooms/" + rid),
      api<GameEvent[]>("/rooms/" + rid + "/events"),
    ])
      .then(([r, e]) => {
        if (active.current === rid && sequence === refreshSequence.current) {
          setRoom(r);
          setEvents(e);
        }
      })
      .catch((e) => {
        if (active.current !== rid || sequence !== refreshSequence.current)
          return;
        if (e instanceof ApiError && (e.status === 404 || e.status === 403))
          removed("Комната удалена или больше недоступна");
        else setError(e.message);
      });
  }, [removed]);
  const { status, processing, actor, send } = useRoomSocket(
    room?.id,
    refresh,
    setError,
    removed,
  );
  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [events.length, processing]);
  const open = (r: Room) => {
    active.current = r.id;
    setRoom(r);
    setScreen("room");
    setTab(r.status === "WAITING" ? "party" : "game");
    setEvents([]);
    setText("");
    refresh();
  };
  const home = () => {
    active.current = null;
    setRoom(null);
    setDeleteTarget(null);
    setText("");
    setScreen("home");
    void run(async () => setRooms(await api<Room[]>("/rooms")));
  };
  const owner = !!room && room.ownerId === user?.id;
  const hero = room?.state.characters.find((h) => h.userId === user?.id);
  const waiting = room?.status === "WAITING";
  const combatHero = room?.state.combat
    ? room.state.characters.find(
        (h) =>
          h.userId === room.state.combatOrder?.[room.state.combatIndex ?? 0],
      )
    : undefined;
  const myTurn = !combatHero || combatHero.userId === user?.id;
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!room?.state.combat) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [room?.state.combat]);
  const skipWait = Math.max(
    0,
    Math.ceil(
      (Date.parse(room?.state.combatTurnSince ?? "") + 60000 - now) / 1000,
    ),
  );
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
    if (!myTurn || processing || room?.status !== "PLAYING") return;
    if (text.trim() && send("player_action", { text })) {
      setText("");
    }
  };
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
      {error && (
        <div className="error banner" role="alert">
          {error}
          <button aria-label="Закрыть ошибку" onClick={() => setError("")}>
            <X size={16} />
          </button>
        </div>
      )}
      {notice && (
        <div className="notice banner" role="status">
          {notice}
          <button aria-label="Закрыть" onClick={() => setNotice("")}>
            <X size={16} />
          </button>
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
                Живой мир, общие решения и ведущий,
                <br />
                который помнит ваши приключения.
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
            {processing && actor && (
              <p className="notice banner" role="status">
                Мастер обрабатывает действие: {actor}
              </p>
            )}
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
                  После каждого хода отвечает противник. При обрыве связи
                  очередь сохраняется. Владелец может пропустить ход после
                  минуты ожидания.
                </p>
                {myTurn && (
                  <button
                    className="secondary"
                    disabled={
                      processing ||
                      status !== "online" ||
                      room.status !== "PLAYING"
                    }
                    onClick={() => send("pass_turn", {})}
                  >
                    Пропустить мой ход
                  </button>
                )}
                {owner && !myTurn && (
                  <button
                    className="secondary"
                    disabled={
                      processing ||
                      status !== "online" ||
                      room.status !== "PLAYING" ||
                      skipWait !== 0
                    }
                    onClick={() => send("skip_turn", { turn: room.state.turn })}
                  >
                    {skipWait > 0
                      ? `Ждём игрока · ${skipWait} с`
                      : `Пропустить ход: ${combatHero.name}`}
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
                            <option>Следопыт</option>
                            <option>Маг</option>
                            <option>Плут</option>
                          </select>
                        </label>
                      </div>
                      <small>
                        В MVP классы описывают образ героя; стартовые
                        характеристики одинаковы.
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
                    disabled={processing || status !== "online"}
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
              <section className="panel">
                <h2>Кампания завершена</h2>
                <p>
                  Весь отряд пал. История сохранена; можно перечитать журнал или
                  начать новую кампанию.
                </p>
              </section>
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
                      .slice(0, 1)
                      .map((q) => (
                        <div key={q.id}>
                          <div className="eyebrow">Ближайшая цель</div>
                          <h3>{q.title}</h3>
                          <p>{q.description}</p>
                        </div>
                      ))}
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
                <div className="journal">
                  {events.map((e) => (
                    <article
                      key={e.id}
                      className={
                        "event " +
                        (e.type === "GM_MESSAGE"
                          ? "gm"
                          : e.type === "PLAYER_ACTION"
                            ? "player"
                            : "mechanics")
                      }
                    >
                      <div className="eyebrow">
                        {e.type === "GM_MESSAGE" ? (
                          <>
                            <Sparkles size={13} />
                            Ведущий
                          </>
                        ) : e.type === "PLAYER_ACTION" ? (
                          "Действие игрока"
                        ) : (
                          <>
                            <Dices size={13} />
                            Правила мира
                          </>
                        )}
                      </div>
                      <p>{e.payload.text}</p>
                    </article>
                  ))}
                  {processing && (
                    <article className="event gm thinking">
                      <Sparkles size={16} />
                      {actor
                        ? `Ведущий обдумывает действие: ${actor}…`
                        : "Ведущий обдумывает ход…"}
                    </article>
                  )}
                  <div ref={bottom} />
                </div>
                <form className="composer" onSubmit={action}>
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
                            disabled={processing}
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
                      waiting ||
                      room.status !== "PLAYING" ||
                      !myTurn ||
                      !hero ||
                      hero.hp <= 0
                    }
                  />
                  <div className="inline">
                    <select
                      aria-label="Кубик"
                      value={dice}
                      onChange={(e) => setDice(e.target.value)}
                    >
                      <option>d20</option>
                      <option>d6</option>
                      <option>2d6+3</option>
                      <option>1d8-1</option>
                    </select>
                    <button
                      type="button"
                      aria-label="Бросить кубик"
                      disabled={
                        processing ||
                        status !== "online" ||
                        room.status !== "PLAYING" ||
                        !hero ||
                        hero.hp <= 0
                      }
                      onClick={() => send("roll_dice", { notation: dice })}
                    >
                      <Dices size={20} />
                    </button>
                    <button
                      className="primary"
                      aria-label="Отправить действие"
                      disabled={
                        processing ||
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
