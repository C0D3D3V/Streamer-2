import { useEffect, useRef } from "react";

interface Props {
  readonly title: string;
  readonly message: string;
  readonly confirmLabel?: string;
  readonly onConfirm: () => void;
  readonly onCancel: () => void;
}

export function ConfirmDialog({ title, message, confirmLabel = "Delete", onConfirm, onCancel }: Props) {
  const dialogRef = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    dialogRef.current?.showModal();
  }, []);

  return (
    <dialog
      ref={dialogRef}
      onClose={onCancel}
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm"
    >
      <div className="w-full max-w-sm bg-surface border border-surface-border rounded-2xl shadow-xl">
        <div className="flex items-center justify-between px-5 py-4 border-b border-surface-border">
          <h2 className="text-white font-semibold">{title}</h2>
          <button onClick={onCancel} className="text-gray-500 hover:text-white transition-colors text-lg leading-none">✕</button>
        </div>
        <div className="p-5 space-y-5">
          <p className="text-gray-400 text-sm">{message}</p>
          <div className="flex gap-3 justify-end">
            <button
              onClick={onCancel}
              className="px-4 py-2 rounded-lg border border-surface-border text-gray-400 hover:text-white hover:border-gray-500 text-sm transition-colors"
            >
              Cancel
            </button>
            <button
              onClick={onConfirm}
              className="px-4 py-2 rounded-lg bg-red-600 hover:bg-red-500 text-white text-sm font-medium transition-colors"
            >
              {confirmLabel}
            </button>
          </div>
        </div>
      </div>
    </dialog>
  );
}
