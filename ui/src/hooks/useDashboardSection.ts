import { useEffect, useRef, type RefObject } from 'react';
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
 * Everything that may be the page's scroller. V2's shell is a full-height flex box with
 * `.main-content` set to `overflow-y: auto`, but whether that or the document ends up
 * scrolling depends on the viewport, so both are handled rather than guessing.
 */
function scrollers(): Element[] {
  return [
    document.scrollingElement,
    document.querySelector('.main-content'),
  ].filter((e): e is Element => !!e);
}

function scrollToTop() {
  scrollers().forEach((s) => s.scrollTo({ top: 0, behavior: 'instant' }));
}

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
 * "The reader scrolls" includes a scroll with no wheel, key or pointer behind it -- a screen
 * reader moving through the page, say. Such a scroll is told apart from the hook's own by
 * where things ended up: after each placement the scroll positions, the section's offset and
 * the dashboard's height are recorded. A scroll event that changed a scroll position AND moved
 * the section while the dashboard kept its height cannot have been ours (our scrolls change
 * none of them once recorded) nor the browser's: when the page changes size the browser
 * scrolls too, clamping at the bottom or anchoring as panels load, and a first version that
 * did not check the height failed an e2e run with Custom Domain Status left 891px down a
 * 720px viewport, most likely by reading one of those as the reader.
 *
 * An unknown section is not an error page: the address is corrected to `/dashboard` and the
 * page shown from the top, the same thing V1 does for `/portal/<unknown>` (#1215). Leaving a
 * section for the plain dashboard -- the sidebar's Overview, the `g d` shortcut, Back --
 * also returns to the top, because that is what asking for the Overview means. Arriving at
 * `/dashboard` any other way (a first load, a `#fragment` link) leaves the scroll alone.
 */
export function useDashboardSection(rootRef: RefObject<HTMLElement | null>) {
  const { section } = useParams();
  const navigate = useNavigate();
  const previous = useRef<string | undefined>(undefined);

  useEffect(() => {
    const came = previous.current;
    previous.current = section;

    if (section === undefined) {
      // Read, not depended on: a fragment changing under a section (the jump nav) is not
      // a new arrival, and must not re-run any of this.
      if (came !== undefined && !window.location.hash) scrollToTop();
      return;
    }

    const anchor = dashboardSectionAnchor(section);
    if (!anchor) {
      // The next run, with no section, scrolls to the top.
      navigate('/dashboard', { replace: true });
      return;
    }

    let placed: {
      positions: number[];
      top: number | null;
      height: number;
    } | null = null;
    const height = () => rootRef.current?.getBoundingClientRect().height ?? 0;
    // 'instant' rather than the page's smooth scrolling: a smooth scroll restarted on every
    // resize never finishes, and arrival is not a transition anyone watches.
    const place = () => {
      const el = document.getElementById(anchor);
      el?.scrollIntoView({ block: 'start', behavior: 'instant' });
      placed = {
        positions: scrollers().map((s) => s.scrollTop),
        top: el ? el.getBoundingClientRect().top : null,
        height: height(),
      };
    };

    let done = false;
    const observer = new ResizeObserver(() => {
      if (!done) place();
    });
    const onScroll = () => {
      if (done || !placed) return;
      // The page changed size since the last placement: this scroll is the browser
      // clamping or anchoring, and the ResizeObserver is about to re-place. Not the reader.
      if (Math.abs(height() - placed.height) > 1) return;
      const now = scrollers().map((s) => s.scrollTop);
      const scrolled = now.some(
        (p, i) => Math.abs(p - placed!.positions[i]) > 1,
      );
      const el = document.getElementById(anchor);
      const moved =
        placed.top === null ||
        !el ||
        Math.abs(el.getBoundingClientRect().top - placed.top) > 2;
      if (scrolled && moved) stop();
    };
    const stop = () => {
      if (done) return;
      done = true;
      observer.disconnect();
      window.clearTimeout(timer);
      READER_EVENTS.forEach((e) => window.removeEventListener(e, stop));
      document.removeEventListener('scroll', onScroll, true);
    };
    const timer = window.setTimeout(stop, SETTLE_MS);
    READER_EVENTS.forEach((e) =>
      window.addEventListener(e, stop, { passive: true }),
    );
    // Capture, because a scroll on .main-content does not bubble to the document.
    document.addEventListener('scroll', onScroll, {
      capture: true,
      passive: true,
    });
    if (rootRef.current) observer.observe(rootRef.current);
    place();

    return stop;
  }, [section, navigate, rootRef]);
}
