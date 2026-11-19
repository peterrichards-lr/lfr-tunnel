import { useEffect, type RefObject } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { dashboardSectionAnchor } from '../dashboardSections';

/**
 * The longest arrival keeps the section in place while the panels above it load. It is a cap,
 * not a delay: re-anchoring only happens when the dashboard changes size, and stops at once
 * when the reader scrolls, types or clicks. Generous because a slow gateway is exactly when
 * the panels arrive late: Registered Subdomains, for one, is not rendered until its panel's
 * data arrives. A 4s cap missed it once on a loaded local test stack.
 */
const SETTLE_MS = 15000;

/** Anything that means the reader has taken over the scroll position. */
const READER_EVENTS = ['wheel', 'touchstart', 'keydown', 'mousedown'] as const;

/**
 * Puts the section named by `/dashboard/<section>` in view (#2527).
 *
 * One scroll on arrival is not enough. Active Tunnels, Reservations Overview and Custom
 * Domain Status each fetch their own data and grow when it arrives, so a section scrolled to
 * on mount is pushed back down the page a moment later -- the URL said tokens and the reader
 * is left looking at reservations. So the section is re-anchored whenever the dashboard
 * changes size, until the reader scrolls, types or clicks, or until SETTLE_MS has passed.
 * A section that is not on the page yet is placed when it appears, by the same rule.
 *
 * An unknown section is not an error page: the address is corrected to `/dashboard` and the
 * page shown from the top, the same thing V1 does for `/portal/<unknown>` (#1215).
 */
export function useDashboardSection(rootRef: RefObject<HTMLElement | null>) {
  const { section } = useParams();
  const navigate = useNavigate();

  useEffect(() => {
    if (section === undefined) return;

    const anchor = dashboardSectionAnchor(section);
    if (!anchor) {
      navigate('/dashboard', { replace: true });
      window.scrollTo({ top: 0, behavior: 'instant' });
      return;
    }

    // 'instant' rather than the page's smooth scrolling: a smooth scroll restarted on every
    // resize never finishes, and arrival is not a transition anyone watches.
    const place = () => {
      document
        .getElementById(anchor)
        ?.scrollIntoView({ block: 'start', behavior: 'instant' });
    };

    let done = false;
    const observer = new ResizeObserver(() => {
      if (!done) place();
    });
    const stop = () => {
      if (done) return;
      done = true;
      observer.disconnect();
      window.clearTimeout(timer);
      READER_EVENTS.forEach((e) => window.removeEventListener(e, stop));
    };
    const timer = window.setTimeout(stop, SETTLE_MS);
    READER_EVENTS.forEach((e) =>
      window.addEventListener(e, stop, { passive: true }),
    );
    if (rootRef.current) observer.observe(rootRef.current);
    place();

    return stop;
  }, [section, navigate, rootRef]);
}
