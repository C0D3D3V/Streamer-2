interface Props {
  title: string;
  message: string;
  confirmLabel?: string;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({ title, message, confirmLabel = "Delete", onConfirm, onCancel }: Props) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm"
      onClick={(e) => { if (e.target === e.currentTarget) onCancel(); }}
    >
      <div className="w-full max-w-sm bg-[#1a1b23] border border-[#2e3042] rounded-2xl shadow-xl">
        <div className="flex items-center justify-between px-5 py-4 border-b border-[#2e3042]">
          <h2 className="text-white font-semibold">{title}</h2>
          <button onClick={onCancel} className="text-gray-500 hover:text-white transition-colors text-lg leading-none">✕</button>
        </div>
        <div className="p-5 space-y-5">
          <p className="text-gray-400 text-sm">{message}</p>
          <div className="flex gap-3 justify-end">
            <button
              onClick={onCancel}
              className="px-4 py-2 rounded-lg border border-[#2e3042] text-gray-400 hover:text-white hover:border-gray-500 text-sm transition-colors"
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
    </div>
  );
}
