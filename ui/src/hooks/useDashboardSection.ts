import { useEffect, type RefObject } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { dashboardSectionAnchor } from '../dashboardSections';

/**
 * How long arrival keeps the section in place while the panels above it load. Long enough
 * for the slowest of the dashboard's own requests on a cold gateway, short enough that it
 * cannot be mistaken for the page fighting the reader.
 */
const SETTLE_MS = 4000;

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
