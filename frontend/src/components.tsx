import { useState } from "react";
import { ArrowRight, Heart, Shield } from "lucide-react";
import type { Hero, Settings } from "./types";
const stats: Record<string, string> = {
  strength: "СИЛ",
  dexterity: "ЛОВ",
  constitution: "ТЕЛ",
  intelligence: "ИНТ",
  wisdom: "МДР",
  charisma: "ХАР",
};
export function SettingsForm({
  initial,
  onSave,
  busy,
}: {
  initial: Settings;
  onSave: (s: Settings) => void;
  busy: boolean;
}) {
  const [s, set] = useState(initial);
  const field = (k: keyof Settings, label: string, area = false) => (
    <label>
      {label}
      {area ? (
        <textarea
          maxLength={k === "worldDescription" ? 4000 : 2000}
          value={s[k]}
          onChange={(e) => set({ ...s, [k]: e.target.value })}
        />
      ) : (
        <input
          required={k === "name"}
          maxLength={80}
          value={s[k]}
          onChange={(e) => set({ ...s, [k]: e.target.value })}
        />
      )}
    </label>
  );
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSave(s);
      }}
      className="stack"
    >
      {field("name", "Название кампании")}
      {field("setting", "Сеттинг")}
      {field("worldDescription", "Мир и начало истории", true)}
      <div className="two">
        {field("tone", "Тон повествования")}
        {field("difficulty", "Сложность")}
      </div>
      <details>
        <summary>Правила и ведущий</summary>
        <div className="stack">
          {field("rules", "Правила", true)}
          {field("gmStyle", "Стиль ведущего")}
          {field("ollamaModel", "Модель Ollama")}
          <label>
            Максимум игроков
            <input
              type="number"
              min="1"
              max="8"
              value={s.maxPlayers}
              onChange={(e) =>
                set({ ...s, maxPlayers: Number(e.target.value) })
              }
            />
          </label>
        </div>
      </details>
      <button className="primary" disabled={busy}>
        {busy ? "Сохраняем…" : "Сохранить кампанию"}
        <ArrowRight size={18} />
      </button>
    </form>
  );
}
export function HeroCard({ hero }: { hero: Hero }) {
  return (
    <section className="panel">
      <div className="eyebrow">
        {hero.race} · {hero.class} · Уровень {hero.level}
      </div>
      <h2>{hero.name}</h2>
      <div className="hero-health">
        <Heart size={18} />
        <strong>
          {hero.hp} / {hero.maxHp}
        </strong>
        <span>HP</span>
        <Shield size={18} />
        <strong>{hero.armorClass}</strong>
      </div>
      <div className="health">
        <i style={{ width: `${(hero.hp / hero.maxHp) * 100}%` }} />
      </div>
      <div className="stats">
        {Object.entries(hero.stats).map(([k, v]) => (
          <div key={k}>
            <small>{stats[k]}</small>
            <strong>{v}</strong>
          </div>
        ))}
      </div>
    </section>
  );
}
