import type { ReactNode } from "react";
import { Check, Info, TriangleAlert, X } from "lucide-react";

type Tone = "info" | "success" | "error";

export function Toast({
  tone,
  title,
  children,
  action,
  onClose,
}: {
  tone: Tone;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  onClose?: () => void;
}) {
  const Icon = tone === "error" ? TriangleAlert : tone === "success" ? Check : Info;
  return (
    <div className={`toast toast-${tone}`} role={tone === "error" ? "alert" : "status"}>
      <Icon className="toast-icon" size={18} aria-hidden="true" />
      <div className="toast-content">
        <strong>{title}</strong>
        {children && <div className="toast-detail">{children}</div>}
        {action && <div className="toast-action">{action}</div>}
      </div>
      {onClose && (
        <button className="toast-close" aria-label="Закрыть оповещение" onClick={onClose}>
          <X size={17} />
        </button>
      )}
    </div>
  );
}
