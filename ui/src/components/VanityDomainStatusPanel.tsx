import { useEffect, useState } from 'react';
import axios from 'axios';
import { useSettings } from '../contexts/SettingsContext';
import { useI18n } from '../contexts/I18nContext';
import Skeleton from './Skeleton';
import StageIcon from './VanityStageIcon';
import SectionHeading from './SectionHeading';

interface VanityDomainStatus {
  full_host: string;
  user_id: string;
  requested_at: string | null;
  nginx_config_at: string | null;
  cert_issued_at: string | null;
  live_at: string | null;
  failed_stage?: string;
  error_message?: string;
  updated_at: string;
}

export default function VanityDomainStatusPanel() {
  const { t } = useI18n();
  const { formatDate } = useSettings();
  const [statuses, setStatuses] = useState<VanityDomainStatus[]>([]);
  const [loading, setLoading] = useState(true);

  const fetchData = async () => {
    try {
      const res = await axios.get('/api/portal/vanity-domain-status');
      setStatuses(res.data || []);
    } catch {
      // Silent -- this panel is a secondary status widget, not the primary reservations
      // flow, and the same 5s-interval pattern used elsewhere in the portal (e.g.
      // AdminSubdomains.tsx) treats a transient poll failure as "try again next tick"
      // rather than surfacing a toast on every failed refresh.
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchData();
    // Provisioning runs over roughly 10-60s (nginx config -> Certbot -> live) -- poll so
    // a domain mid-setup visibly progresses without the user needing to refresh the page.
    const interval = setInterval(fetchData, 5000);
    return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // stageIcon renders one column's icon for a given status row: failed takes precedence
  // over the stage's own (always-null-on-failure) timestamp, since MarkVanityDomainFailed
  // never sets the failed stage's own timestamp -- it just records which stage and why.
  const stageIcon = (
    status: VanityDomainStatus,
    stage: string,
    timestamp: string | null,
  ) => {
    if (status.failed_stage === stage) {
      return (
        <StageIcon
          state="failed"
          title={status.error_message || t('vanity_status_failed', 'Failed')}
        />
      );
    }
    if (timestamp) {
      return <StageIcon state="done" title={formatDate(timestamp)} />;
    }
    return (
      <StageIcon
        state="open"
        title={t('vanity_status_not_reached', 'Not yet reached')}
      />
    );
  };

  const summaryFor = (status: VanityDomainStatus) => {
    if (status.failed_stage) {
      return `${t('vanity_status_summary_failed', 'Failed')} (${status.failed_stage}): ${status.error_message || t('vanity_status_unknown_error', 'Unknown error')}`;
    }
    if (status.live_at) {
      return `${t('vanity_status_summary_live', 'Live since')} ${formatDate(status.live_at)}`;
    }
    if (status.cert_issued_at) {
      return t(
        'vanity_status_summary_cert',
        'Certificate issued, finishing setup...',
      );
    }
    if (status.nginx_config_at) {
      return t('vanity_status_summary_nginx', 'Requesting certificate...');
    }
    return t('vanity_status_summary_requested', 'Setup starting...');
  };

  if (loading) {
    return (
      <div className="card p-0">
        <div className="p-xl border-b">
          <Skeleton width={220} height={24} />
        </div>
        <div className="p-xl">
          <Skeleton width="100%" height={16} />
        </div>
      </div>
    );
  }

  if (statuses.length === 0) {
    return null; // Nothing to show for users with no custom-domain attempts -- avoids an empty card taking up space next to Subdomain Reservations.
  }

  return (
    <div className="card p-0">
      <div className="p-xl border-b">
        <SectionHeading
          anchor="custom-domain-status"
          className="section-title m-0"
          label={t('vanity_status_title', 'Custom Domain Status')}
        />
        <p className="text-muted text-sm mt-xs mb-0">
          {t(
            'vanity_status_desc',
            'Live provisioning progress for your custom domains.',
          )}
        </p>
      </div>
      <div className="table-responsive">
        <table className="w-full">
          <thead>
            <tr className="border-b text-left">
              <th className="th-col">
                {t('vanity_status_col_domain', 'Domain')}
              </th>
              <th className="th-col text-center">
                {t('vanity_status_col_requested', 'Requested')}
              </th>
              <th className="th-col text-center">
                {t('vanity_status_col_nginx', 'Nginx Config')}
              </th>
              <th className="th-col text-center">
                {t('vanity_status_col_cert', 'Cert Issued')}
              </th>
              <th className="th-col text-center">
                {t('vanity_status_col_live', 'Live')}
              </th>
              <th className="th-col">
                {t('vanity_status_col_summary', 'Summary')}
              </th>
            </tr>
          </thead>
          <tbody>
            {statuses.map((status) => (
              <tr key={status.full_host} className="border-b">
                <td className="td-cell font-mono">{status.full_host}</td>
                <td className="td-cell text-center">
                  {status.requested_at ? (
                    <StageIcon
                      state="done"
                      title={formatDate(status.requested_at)}
                    />
                  ) : (
                    <StageIcon
                      state="open"
                      title={t('vanity_status_not_reached', 'Not yet reached')}
                    />
                  )}
                </td>
                <td className="td-cell text-center">
                  {stageIcon(status, 'nginx_config', status.nginx_config_at)}
                </td>
                <td className="td-cell text-center">
                  {stageIcon(status, 'cert_issued', status.cert_issued_at)}
                </td>
                <td className="td-cell text-center">
                  {stageIcon(status, 'live', status.live_at)}
                </td>
                <td className="td-cell text-sm text-muted">
                  {summaryFor(status)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
