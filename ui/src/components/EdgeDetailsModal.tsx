import { useEffect, useState } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';
import { useSettings } from '../contexts/SettingsContext';

interface EdgeDetailsModalProps {
  nodeId: string;
  status: string;
  resolvedIPv4?: string;
  resolvedIPv6?: string;
  latencyMs?: number;
  lastCheckAt?: number;
  version?: string;
  errorMessage?: string;
  powerActionsEnabled: boolean;
  onClose: () => void;
}

interface Schedule {
  enabled: boolean;
  stop_time?: string;
  start_time?: string;
  timezone?: string;
}

// Read-only detail view for a single edge node's health status -- carries
// forward the V1 dashboard's "Details" (error message) and IP-address
// columns into V2 (#886), as a modal rather than two more always-visible
// table columns, and shows both IPv4 and IPv6 when a node is dual-stack
// rather than picking one arbitrarily (see the backend fix in
// server_edge.go's resolveIPv4AndIPv6).
export default function EdgeDetailsModal({
  nodeId, status, resolvedIPv4, resolvedIPv6, latencyMs, lastCheckAt, version, errorMessage, powerActionsEnabled, onClose,
}: EdgeDetailsModalProps) {
  const { t } = useI18n();
  const { formatDate } = useSettings();
  const [schedule, setSchedule] = useState<Schedule | null>(null);
  const [scheduleLoading, setScheduleLoading] = useState(powerActionsEnabled);

  useEffect(() => {
    if (!powerActionsEnabled) return;
    let cancelled = false;
    (async () => {
      try {
        const res = await axios.get(`/api/admin/edge/${encodeURIComponent(nodeId)}/schedule`);
        if (!cancelled) setSchedule(res.data);
      } catch {
        // No schedule configured for this node, or the sidecar rejected it --
        // either way, just show nothing rather than an error in a read-only view.
      } finally {
        if (!cancelled) setScheduleLoading(false);
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodeId, powerActionsEnabled]);

  const row = (label: string, value: string) => (
    <div className="flex justify-between gap-lg py-xs border-b text-sm">
      <span className="text-muted">{label}</span>
      <span className="td-cell--mono text-right">{value}</span>
    </div>
  );

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="modal-card modal-card--md"
        onClick={e => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby="edge-details-modal-title"
      >
        <div className="modal-header mb-md">
          <h2 id="edge-details-modal-title" className="modal-title text-md">{nodeId}</h2>
          <button type="button" onClick={onClose} className="modal-close" aria-label={t('close', 'Close')}>×</button>
        </div>

        {row(t('status', 'Status'), status || '—')}
        {row('IPv4', resolvedIPv4 || '—')}
        {row('IPv6', resolvedIPv6 || '—')}
        {row(t('latency', 'Latency'), latencyMs ? `${latencyMs}ms` : '—')}
        {row(t('version', 'Version'), version || '—')}
        {row(t('last_check', 'Last Check'), lastCheckAt ? formatDate(new Date(lastCheckAt * 1000).toISOString()) : '—')}

        {powerActionsEnabled && (
          <>
            <div className="mt-md mb-xs text-2xs text-muted uppercase tracking-wider">
              {t('edge_schedule_modal_title', 'Stop/Start Schedule')}
            </div>
            {scheduleLoading ? (
              row(t('loading', 'Loading...'), '')
            ) : schedule?.enabled ? (
              <>
                {row(t('edge_schedule_stop_time', 'Stop time (local)'), schedule.stop_time || '—')}
                {row(t('edge_schedule_start_time', 'Start time (local)'), schedule.start_time || '—')}
                {row(t('edge_schedule_timezone', 'Timezone'), schedule.timezone || '—')}
              </>
            ) : (
              row(t('edge_schedule_status', 'Status'), t('edge_schedule_disabled', 'Disabled / not configured'))
            )}
          </>
        )}

        {errorMessage && (
          <div className="mt-md p-md rounded-sm text-xs alert-banner alert-banner--danger">
            {errorMessage}
          </div>
        )}

        <div className="flex justify-end mt-lg">
          <button type="button" className="btn btn-secondary" onClick={onClose}>{t('close', 'Close')}</button>
        </div>
      </div>
    </div>
  );
}
