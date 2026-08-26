import { useEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

interface ActionMenuProps {
  buttonLabel?: ReactNode;
  buttonTitle?: string;
  buttonClassName?: string;
  align?: 'left' | 'right';
  children: (close: () => void) => ReactNode;
}

// Row-action dropdown for tables. Renders its menu into a portal on
// document.body with a viewport-computed `position: fixed`, instead of
// `position: absolute` inside the table cell -- an absolutely-positioned
// menu is clipped by the table's scroll container (`.table-responsive` sets
// overflow-x: auto, which implicitly makes overflow-y auto too) whenever it
// opens near the bottom of the visible table, and scrolling afterwards can
// leave it stranded off-screen entirely. This mirrors the pattern the V1
// dashboard's toggleActionMenu() already uses (dashboard.js), which doesn't
// have this bug for the same reason -- move to body, position via
// getBoundingClientRect, close on any scroll.
export default function ActionMenu({
  buttonLabel = '⋮',
  buttonTitle,
  buttonClassName = 'btn btn-secondary text-xs py-xs px-sm',
  align = 'right',
  children,
}: ActionMenuProps) {
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  const close = () => setOpen(false);

  const openMenu = () => {
    const btn = btnRef.current;
    if (!btn) return;
    const rect = btn.getBoundingClientRect();
    const menuWidth = menuRef.current?.offsetWidth || 200;
    const menuHeight = menuRef.current?.offsetHeight || 160;

    let left = align === 'right' ? rect.right - menuWidth : rect.left;
    left = Math.max(10, Math.min(left, window.innerWidth - 10 - menuWidth));

    let top = rect.bottom + 4;
    if (top + menuHeight > window.innerHeight - 10) {
      top = rect.top - menuHeight - 4;
    }

    setPos({ top, left });
    setOpen(true);
  };

  useEffect(() => {
    if (!open) return;
    // Recompute once mounted so menuRef's real size is used instead of the
    // fallback estimate above (matters most for the upward-flip decision).
    const btn = btnRef.current;
    if (btn) {
      const rect = btn.getBoundingClientRect();
      const menuWidth = menuRef.current?.offsetWidth || 200;
      const menuHeight = menuRef.current?.offsetHeight || 160;
      let left = align === 'right' ? rect.right - menuWidth : rect.left;
      left = Math.max(10, Math.min(left, window.innerWidth - 10 - menuWidth));
      let top = rect.bottom + 4;
      if (top + menuHeight > window.innerHeight - 10) {
        top = rect.top - menuHeight - 4;
      }
      setPos({ top, left });
    }

    const handleScroll = () => close();
    const handleResize = () => close();
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') close();
    };
    window.addEventListener('scroll', handleScroll, {
      capture: true,
      passive: true,
    });
    window.addEventListener('resize', handleResize, { passive: true });
    window.addEventListener('keydown', handleKey);
    return () => {
      window.removeEventListener('scroll', handleScroll, { capture: true });
      window.removeEventListener('resize', handleResize);
      window.removeEventListener('keydown', handleKey);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  return (
    <>
      <button
        ref={btnRef}
        type="button"
        className={buttonClassName}
        title={buttonTitle}
        onClick={() => (open ? close() : openMenu())}
      >
        {buttonLabel}
      </button>
      {open &&
        pos &&
        createPortal(
          <>
            <div className="fixed inset-0 z-40" onClick={close} />
            <div
              ref={menuRef}
              className="table-column-dropdown"
              style={{
                position: 'fixed',
                top: pos.top,
                left: pos.left,
                right: 'auto',
              }}
            >
              {children(close)}
            </div>
          </>,
          document.body,
        )}
    </>
  );
}
