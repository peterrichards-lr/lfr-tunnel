import { useEffect, useState } from 'react';
import axios from 'axios';

/**
 * Pending-item counts for the admin approval queues, shown as sidebar badges (#2427).
 *
 * Registrations were the only queue with a badge, so an extension or token-permanence request
 * sat unseen until an admin happened to open its page.
 */
export type AdminQueue = 'registrations' | 'extensions' | 'permanence';

/** `null` is "not known" -- a failed fetch -- and renders no badge rather than claiming zero. */
export type AdminQueueCounts = Record<AdminQueue, number | null>;

const EVENT = 'lfr:admin-queue-count';

/**
 * Called by a queue page after every successful load of its list, so the badge always agrees
 * with the list the admin is looking at and drops the moment a decision lands -- without the
 * sidebar refetching anything.
 */
export function reportAdminQueueCount(queue: AdminQueue, count: number) {
  window.dispatchEvent(new CustomEvent(EVENT, { detail: { queue, count } }));
}

const countOf = async (
  url: string,
  keep: (item: any) => boolean = () => true,
) => {
  try {
    const res = await axios.get(url);
    return ((res.data || []) as any[]).filter(keep).length;
  } catch (err) {
    console.error(`Failed to load ${url} for sidebar badge`, err);
    return null;
  }
};

/**
 * `refreshKey` changing triggers a refetch -- the sidebar passes the route, so a request that
 * arrived since the last look shows up on the admin's next navigation. Deliberately no timer:
 * the registrations badge used to fetch the whole user list every 10 seconds (#1208).
 */
export function useAdminQueueCounts(enabled: boolean, refreshKey: string) {
  const [counts, setCounts] = useState<AdminQueueCounts>({
    registrations: null,
    extensions: null,
    permanence: null,
  });

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    Promise.all([
      // `pending` only, matching the V2 Registrations tab this badge points at.
      countOf('/api/admin/users', (u) => u.status === 'pending'),
      countOf('/api/admin/reservations/extensions'),
      countOf('/api/admin/tokens/permanence-requests'),
    ]).then(([registrations, extensions, permanence]) => {
      if (!cancelled) setCounts({ registrations, extensions, permanence });
    });
    return () => {
      cancelled = true;
    };
  }, [enabled, refreshKey]);

  useEffect(() => {
    const onReport = (e: Event) => {
      const { queue, count } = (e as CustomEvent).detail;
      setCounts((prev) => ({ ...prev, [queue]: count }));
    };
    window.addEventListener(EVENT, onReport);
    return () => window.removeEventListener(EVENT, onReport);
  }, []);

  return counts;
}
