import { useLocale } from "./locale";
import { useLayoutEffect, useRef, type ReactNode } from "react";
import { X } from "@phosphor-icons/react";

export function Dialog({ title, children, close, drawer, locked, className }: {
  title: string;
  children: ReactNode;
  close: () => void;
  drawer?: boolean;
  className?: string;
  locked?: boolean;
}) {
  const { t } = useLocale();
  const ref = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    const dialog = ref.current;
    const opener = document.activeElement;
    dialog?.showModal();
    dialog?.querySelector<HTMLElement>("[data-dialog-focus]")?.focus({ preventScroll: true });
    return () => {
      dialog?.close();
      if (opener instanceof HTMLElement && opener.isConnected) opener.focus({ preventScroll: true });
    };
  }, []);
  return (
    <dialog ref={ref} className={[drawer && "workspace-drawer", className].filter(Boolean).join(" ")}
      onCancel={event => { event.preventDefault(); if (!locked) close(); }} aria-label={title}>
      <div className="dialog-header"><h2>{title}</h2>
        <button className="icon-button" type="button" disabled={locked} onClick={close} aria-label={t("Close")}><X size={22} /></button>
      </div>
      {children}
    </dialog>
  );
}
