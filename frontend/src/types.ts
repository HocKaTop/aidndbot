export type User = { id: number; firstName: string; username: string };
export type Settings = {
  name: string;
  setting: string;
  worldDescription: string;
  tone: string;
  rules: string;
  difficulty: string;
  gmStyle: string;
  ollamaModel: string;
  maxPlayers: number;
};
export type Item = {
  id: string;
  name: string;
  description: string;
  quantity: number;
  type: string;
};
export type Hero = {
  id: string;
  userId: number;
  name: string;
  race: string;
  class: string;
  classId?: "warrior" | "rogue" | "mage";
  level: number;
  experience?: number;
  hp: number;
  maxHp: number;
  armorClass: number;
  resource?: number;
  resourceMax?: number;
  stats: Record<string, number>;
  inventory: Item[];
};
export type Room = {
  id: string;
  code: string;
  ownerId: number;
  status: string;
  inviteUrl: string;
  members: { userId: number; name: string; role: string; ready: boolean }[];
  state: {
    pendingQuestCompletion?: { questId: string; reason: string };
    ending?: {
      reason: "owner" | "defeat" | "objective";
      note: string;
      finishedAt: string;
    };
    settings: Settings;
    characters: Hero[];
    npcs: {
      id: string;
      name: string;
      description: string;
      hp: number;
      maxHp: number;
      alive: boolean;
      disposition: string;
      location?: string;
    }[];
    quests: {
      id: string;
      title: string;
      description: string;
      status: string;
    }[];
    scene: { title: string; description: string; location: string } | null;
    combat: boolean;
    combatOrder?: number[];
    combatIndex?: number;
    combatRound?: number;
    combatTurnSince?: string;
    turn: number;
  };
};
export type GameEvent = {
  id: string;
  type: string;
  createdAt: string;
  payload: { text?: string; roll?: { notation: string; total: number } };
};
export type HistoryEvent = GameEvent & { sequence: string };
export type HistoryPage = { events: HistoryEvent[]; nextCursor: string | null };
