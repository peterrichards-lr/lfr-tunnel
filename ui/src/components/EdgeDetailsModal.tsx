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
  onClose: () => void;
}

// Read-only detail view for a single edge node's health status -- carries
// forward the V1 dashboard's "Details" (error message) and IP-address
// columns into V2 (#886), as a modal rather than two more always-visible
// table columns, and shows both IPv4 and IPv6 when a node is dual-stack
// rather than picking one arbitrarily (see the backend fix in
// server_edge.go's resolveIPv4AndIPv6).
export default function EdgeDetailsModal({
  nodeId, status, resolvedIPv4, resolvedIPv6, latencyMs, lastCheckAt, version, errorMessage, onClose,
}: EdgeDetailsModalProps) {
  const { t } = useI18n();
  const { formatDate } = useSettings();

  const row = (label: string, value: string) => (
    <div className="flex justify-between gap-lg py-xs border-b" style={{ fontSize: 13 }}>
      <span className="text-muted">{label}</span>
      <span className="td-cell--mono" style={{ textAlign: 'right' }}>{value}</span>
    </div>
  );

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="modal-card modal-card--md p-xl"
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

        {errorMessage && (
          <div className="mt-md p-md rounded-sm text-xs" style={{ background: 'rgba(239, 68, 68, 0.1)', color: 'var(--danger)' }}>
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
