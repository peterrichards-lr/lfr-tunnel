import { useEffect, useRef, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

/**
 * The backdrop and dismissal behaviour every modal shares (#1558).
 *
 * ## Why this renders through a portal
 *
 * `.modal-backdrop` is `position: fixed`, which normally resolves against the viewport -- but
 * only while no ancestor establishes a containing block. A non-`none` `transform` does, and
 * `.animate-fade-in` leaves one behind permanently:
 *
 *     .animate-fade-in { animation: fadeInUp 0.6s ... both; }
 *
 * `fill-mode: both` retains the final keyframe, whose `translateY(0)` computes to
 * `matrix(1, 0, 0, 1, 0, 0)`. It has no visual effect, so nothing looks wrong, but every fixed
 * descendant is now positioned against that div instead of the viewport.
 *
 * On the dashboard that div is ~1623px tall, so a "centred" dialog landed at y≈801 -- below the
 * fold on a 720px viewport. The backdrop was visible and the dialog was not, which is exactly
 * how it was reported: an overlay with no content.
 *
 * Rendering into `document.body` puts the modal outside any such ancestor. That is the durable
 * fix rather than removing the one offending transform: the next `transform`, `filter`,
 * `will-change` or `contain` added anywhere up the tree would reintroduce it, and it would look
 * like a layout glitch rather than the CSS containing-block rule it is.
 *
 * Worth knowing: `@media (prefers-reduced-motion: reduce)` sets `animation: none`, which removes
 * the transform and hides the bug entirely. Anyone testing with reduced motion on -- a common
 * accessibility setting -- would never reproduce it.
 *
 * ## Why Escape and focus live here
 *
 * The dialog previously had no Escape handler, so when it rendered off-screen there was no way
 * out except reloading the page. Escape is what a user tries when a dialog will not close, and
 * putting it in the shell means no future modal can forget it.
 */
export default function ModalShell({
  isOpen,
  onClose,
  labelledBy,
  cardClassName = 'modal-card',
  children,
}: {
  isOpen: boolean;
  onClose: () => void;
  labelledBy?: string;
  cardClassName?: string;
  children: ReactNode;
}) {
  const cardRef = useRef<HTMLDivElement>(null);
  // Captured on open so focus can go back where it came from. Returning focus to <body> would
  // send a keyboard user to the top of the document and make them navigate back each time.
  const openerRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!isOpen) return;

    openerRef.current = document.activeElement as HTMLElement | null;
    document.body.style.overflow = 'hidden';

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation();
        onClose();
      }
    };
    document.addEventListener('keydown', onKeyDown);

    // Move focus into the dialog so Escape and Tab act on it rather than on the page behind.
    cardRef.current?.focus();

    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = 'unset';
      openerRef.current?.focus?.();
    };
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  return createPortal(
    <div className="modal-backdrop" onClick={onClose}>
      <div
        ref={cardRef}
        className={cardClassName}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby={labelledBy}
        tabIndex={-1}
      >
        {children}
      </div>
    </div>,
    document.body,
  );
}
