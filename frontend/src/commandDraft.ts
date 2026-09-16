export type Packet = {
  id: string;
  type: string;
  data: Record<string, unknown>;
  expectedTurn?: number;
};
export type DeliveryState =
  "sent" | "queued" | "processing" | "unknown" | "failed" | "completed";
export type Delivery = {
  packet: Packet;
  status: DeliveryState;
  message?: string;
};
export type Draft = { key: string; text: string; delivery: Delivery | null };
export const unresolved = (delivery: Delivery | null) =>
  !!delivery && delivery.status !== "failed" && delivery.status !== "completed";

export function loadDraft(key: string): Draft {
  const empty = { key, text: "", delivery: null };
  if (!key) return empty;
  try {
    const saved = JSON.parse(localStorage.getItem(key) || "null");
    if (!saved || typeof saved.text !== "string") return empty;
    const delivery = saved.delivery;
    const valid =
      delivery &&
      typeof delivery.packet?.id === "string" &&
      typeof delivery.packet?.type === "string" &&
      delivery.packet?.data &&
      typeof delivery.packet.data === "object";
    return {
      key,
      text: saved.text.slice(0, 1000),
      delivery: valid
        ? {
            ...delivery,
            status: delivery.status === "failed" ? "failed" : "unknown",
          }
        : null,
    };
  } catch {
    return empty;
  }
}

export function saveDraft(draft: Draft) {
  if (!draft.key) return;
  try {
    const delivery =
      draft.delivery?.status === "completed" ? null : draft.delivery;
    if (!draft.text && !delivery) localStorage.removeItem(draft.key);
    else
      localStorage.setItem(
        draft.key,
        JSON.stringify({ text: draft.text, delivery }),
      );
  } catch {
    /* Storage may be unavailable; keep the draft in memory. */
  }
}
