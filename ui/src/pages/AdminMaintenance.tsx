import { useCallback, useEffect, useState } from 'react';
import axios from 'axios';
import { useOutletContext } from 'react-router-dom';
import { useI18n } from '../contexts/I18nContext';
import ModalShell from '../components/ModalShell';
import Skeleton from '../components/Skeleton';

/**
 * Gateway Maintenance (#1568), the V2 counterpart of V1's screen.
 *
 * Two independent modes behind one endpoint:
 *
 *   soft  -- blocks standard logins, rejects new tunnels and kicks active ones. Admins and
 *            owners stay unblocked, which is what makes it reversible from the portal.
 *   hard  -- the nginx "iron curtain", which blocks every external request including this
 *            portal. Owner-only, and can only be undone over SSH.
 *
 * `status` from GET /api/admin/maintenance is tri-state -- "true", "false" or "pending" --
 * where pending means scheduled but not yet started. Treating it as a boolean would render a
 * scheduled window as off, which is the state an operator most needs to see, so it is modelled
 * as the three values it actually has.
 */
type MaintStatus = 'true' | 'false' | 'pending';

interface MaintenanceState {
  status: MaintStatus;
  iron_curtain: boolean;
  action: string;
  reason: string;
  duration: number;
  start_time?: string;
}

interface PendingConfirm {
  hard: boolean;
  enabling: boolean;
  countdown: number;
}

export default function AdminMaintenance() {
  const { user } = useOutletContext<{ user: any }>();
  const { t } = useI18n();

  const [state, setState] = useState<MaintenanceState | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState<PendingConfirm | null>(null);

  // V1's defaults, kept identical so the same click produces the same window in either portal.
  const [softAction, setSoftAction] = useState('Server Upgrade');
  const [softDuration, setSoftDuration] = useState(30);
  const [softReason, setSoftReason] = useState(
    'System upgrade and maintenance',
  );
  const [countdown, setCountdown] = useState(5);

  const [hardAction, setHardAction] = useState('Database Restoration');
  const [hardDuration, setHardDuration] = useState(60);
  const [hardReason, setHardReason] = useState(
    'Performing database restore operations.',
  );

  const isOwner = user?.role === 'owner';

  const load = useCallback(async () => {
    try {
      const res = await axios.get('/api/admin/maintenance');
      setState({
        status: (res.data?.status ?? 'false') as MaintStatus,
        iron_curtain: !!res.data?.iron_curtain,
        action: res.data?.action ?? '',
        reason: res.data?.reason ?? '',
        duration: Number(res.data?.duration ?? 0),
        start_time: res.data?.start_time,
      });
      setError('');
    } catch (err: any) {
      setError(
        err.response?.data?.error ||
          err.message ||
          t('maint_load_failed', 'Failed to load maintenance status.'),
      );
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    load();
  }, [load]);

  const softActive = state?.status === 'true';
  const softPending = state?.status === 'pending';
  const softOn = softActive || softPending;

  const apply = async ({ hard, enabling, countdown: cd }: PendingConfirm) => {
    setBusy(true);
    try {
      const payload: Record<string, unknown> = {
        enabled: enabling,
        iron_curtain: hard,
        action: hard ? hardAction : softAction,
        reason: hard ? hardReason : softReason,
        duration: hard ? hardDuration : softDuration,
      };
      // Only sent when scheduling ahead, matching V1: an unconditional 0 would be a valid
      // "start now" instruction rather than the absence of a schedule.
      if (enabling && !hard && cd > 0) {
        payload.countdown_minutes = cd;
      }
      await axios.post('/api/admin/maintenance', payload);
      await load();
    } catch (err: any) {
      setError(
        err.response?.data?.error ||
          err.message ||
          t('maint_change_failed', 'Failed to change maintenance mode.'),
      );
    } finally {
      setBusy(false);
      setConfirming(null);
    }
  };

  const confirmText = (c: PendingConfirm) => {
    if (c.hard) {
      return t(
        'maint_confirm_hard',
        'This blocks every external request, including this portal. You will be disconnected, and it can only be disabled over SSH.',
      );
    }
    if (!c.enabling) {
      return t(
        'maint_confirm_off',
        'This restores standard gateway routing, logins and tunnel connections.',
      );
    }
    return c.countdown > 0
      ? t(
          'maint_confirm_schedule',
          'Users will see a warning banner, and maintenance will begin when the countdown reaches zero.',
        )
      : t(
          'maint_confirm_now',
          'This immediately closes all standard tunnels, rejects new connections and blocks standard logins.',
        );
  };

  if (loading) {
    return (
      <div className="animate-fade-in">
        <Skeleton width={220} height={28} />
        <Skeleton width={320} height={16} className="mt-sm" />
        <div className="card p-xl mt-xl">
          <Skeleton width="100%" height={140} />
        </div>
      </div>
    );
  }

  const statusBadge = () => {
    if (softActive)
      return (
        <span className="badge badge-danger">
          {t('maint_status_active', 'Active')}
        </span>
      );
    if (softPending)
      return (
        <span className="badge badge-warning">
          {t('maint_status_scheduled', 'Scheduled')}
        </span>
      );
    return (
      <span className="badge badge-success">
        {t('maint_status_inactive', 'Inactive')}
      </span>
    );
  };

  return (
    <div className="animate-fade-in">
      <div className="page-header">
        <div>
          <h1 className="page-header__title">
            {t('maint_tab_title', 'Gateway Maintenance')}
          </h1>
          <p className="page-header__desc">
            {t(
              'maint_desc',
              'Take the gateway offline for planned work, immediately or on a countdown.',
            )}
          </p>
        </div>
      </div>

      {error && (
        <div className="alert-banner alert-banner--danger mb-xl">{error}</div>
      )}

      <div className="card p-xl mb-xl">
        <div className="flex items-center justify-between gap-md mb-md flex-wrap">
          <h3 className="text-lg fw-bold m-0">
            🛠️ {t('maint_soft_title', 'Gateway Soft Maintenance Mode')}
          </h3>
          <div data-testid="soft-status">{statusBadge()}</div>
        </div>

        <p className="text-muted text-sm mb-lg">
          {t(
            'maint_soft_desc',
            'Blocks standard user logins, rejects new tunnel connections, and kicks active standard tunnels. Administrators and owners stay unblocked, so this mode can always be turned off from the portal.',
          )}
        </p>

        {softOn && state?.start_time && (
          <p className="text-sm mb-lg">
            {t('maint_window', 'Window')}: {state.action || '—'} ·{' '}
            {state.duration}
            {t('minutes_short', 'm')} · {state.reason}
          </p>
        )}

        {/* Hidden once on, matching V1: the fields describe a window that has already been
            submitted, so editing them here would imply an effect they do not have. */}
        {!softOn && (
          <div className="grid gap-md mb-lg" style={{ maxWidth: 560 }}>
            <div className="flex gap-md flex-wrap">
              <div className="flex-1">
                <label className="form-label" htmlFor="maint-soft-action">
                  {t('maint_lbl_action', 'Action Name')}
                </label>
                <input
                  id="maint-soft-action"
                  className="input-field"
                  value={softAction}
                  onChange={(e) => setSoftAction(e.target.value)}
                />
              </div>
              <div className="flex-1">
                <label className="form-label" htmlFor="maint-soft-duration">
                  {t('maint_lbl_duration', 'Duration (Minutes)')}
                </label>
                <input
                  id="maint-soft-duration"
                  type="number"
                  min={1}
                  className="input-field"
                  value={softDuration}
                  onChange={(e) =>
                    setSoftDuration(Number(e.target.value) || 30)
                  }
                />
              </div>
            </div>
            <div>
              <label className="form-label" htmlFor="maint-soft-reason">
                {t('maint_lbl_reason', 'Reason / Details')}
              </label>
              <input
                id="maint-soft-reason"
                className="input-field"
                value={softReason}
                onChange={(e) => setSoftReason(e.target.value)}
              />
            </div>
          </div>
        )}

        <div className="flex gap-md items-center flex-wrap">
          {!softOn && (
            <select
              id="maint-countdown-select"
              className="input-field w-auto m-0"
              value={countdown}
              onChange={(e) => setCountdown(Number(e.target.value))}
              aria-label={t(
                'aria_maint_countdown',
                'Countdown before maintenance starts',
              )}
            >
              <option value={0}>
                {t('maint_opt_0', 'Immediate Activation')}
              </option>
              <option value={1}>
                {t('maint_opt_1', 'Schedule in 1 Minute')}
              </option>
              <option value={5}>
                {t('maint_opt_5', 'Schedule in 5 Minutes')}
              </option>
              <option value={10}>
                {t('maint_opt_10', 'Schedule in 10 Minutes')}
              </option>
              <option value={30}>
                {t('maint_opt_30', 'Schedule in 30 Minutes')}
              </option>
            </select>
          )}
          <button
            id="btn-toggle-maint"
            type="button"
            className={`btn w-auto ${softOn ? 'btn-outline' : 'btn-primary'}`}
            disabled={busy}
            onClick={() =>
              setConfirming({ hard: false, enabling: !softOn, countdown })
            }
          >
            {softOn
              ? t('maint_btn_disable_soft', 'Disable Soft Maintenance')
              : t('maint_btn_enable_soft', 'Enable Soft Maintenance')}
          </button>
        </div>
      </div>

      {/* Owner-only, as in V1. Gated on role rather than merely hidden, because this is the
          control that cannot be undone from the portal. */}
      {isOwner && (
        <div className="card p-xl border-danger" data-testid="iron-curtain">
          <div className="flex items-center justify-between gap-md mb-md flex-wrap">
            <h3 className="text-lg fw-bold m-0 text-danger">
              🔒{' '}
              {t(
                'maint_iron_title',
                'Nginx Iron Curtain Mode (Hard Maintenance — Owner Only)',
              )}
            </h3>
            <span
              className={`badge ${state?.iron_curtain ? 'badge-danger' : 'badge-success'}`}
            >
              {state?.iron_curtain
                ? t('maint_status_active', 'Active')
                : t('maint_status_inactive', 'Inactive')}
            </span>
          </div>

          <p className="text-muted text-sm mb-lg">
            {t('maint_iron_desc', 'Blocks all external incoming requests.')}{' '}
            <strong className="text-danger">
              {t(
                'maint_iron_warn',
                'You will be disconnected. It must be disabled via SSH.',
              )}
            </strong>
          </p>

          {!state?.iron_curtain && (
            <div className="grid gap-md mb-lg" style={{ maxWidth: 560 }}>
              <div className="flex gap-md flex-wrap">
                <div className="flex-1">
                  <label className="form-label" htmlFor="maint-hard-action">
                    {t('maint_lbl_action', 'Action Name')}
                  </label>
                  <input
                    id="maint-hard-action"
                    className="input-field"
                    value={hardAction}
                    onChange={(e) => setHardAction(e.target.value)}
                  />
                </div>
                <div className="flex-1">
                  <label className="form-label" htmlFor="maint-hard-duration">
                    {t('maint_lbl_duration', 'Duration (Minutes)')}
                  </label>
                  <input
                    id="maint-hard-duration"
                    type="number"
                    min={1}
                    className="input-field"
                    value={hardDuration}
                    onChange={(e) =>
                      setHardDuration(Number(e.target.value) || 60)
                    }
                  />
                </div>
              </div>
              <div>
                <label className="form-label" htmlFor="maint-hard-reason">
                  {t('maint_lbl_reason', 'Reason / Details')}
                </label>
                <input
                  id="maint-hard-reason"
                  className="input-field"
                  value={hardReason}
                  onChange={(e) => setHardReason(e.target.value)}
                />
              </div>
            </div>
          )}

          <button
            id="btn-toggle-hard-maint"
            type="button"
            className={`btn w-auto ${state?.iron_curtain ? 'btn-outline' : 'btn-danger'}`}
            disabled={busy}
            onClick={() =>
              setConfirming({
                hard: true,
                enabling: !state?.iron_curtain,
                countdown: 0,
              })
            }
          >
            {state?.iron_curtain
              ? t('maint_btn_disable_hard', 'Disable Iron Curtain')
              : t('maint_btn_enable_hard', 'Enable Iron Curtain')}
          </button>
        </div>
      )}

      <ModalShell
        isOpen={confirming !== null}
        onClose={() => setConfirming(null)}
        labelledBy="maint-confirm-title"
        cardClassName="modal-card modal-card--sm"
      >
        {confirming && (
          <>
            <div className="modal-header">
              <h3 id="maint-confirm-title" className="modal-title">
                {confirming.enabling
                  ? t('maint_confirm_enable_title', 'Enable maintenance?')
                  : t('maint_confirm_disable_title', 'Disable maintenance?')}
              </h3>
            </div>
            <p className="text-sm text-muted">{confirmText(confirming)}</p>
            <div className="flex gap-md justify-end mt-lg">
              <button
                type="button"
                className="btn btn-secondary w-auto"
                onClick={() => setConfirming(null)}
              >
                {t('cancel', 'Cancel')}
              </button>
              <button
                type="button"
                className={`btn w-auto ${confirming.enabling ? 'btn-danger' : 'btn-primary'}`}
                disabled={busy}
                onClick={() => apply(confirming)}
              >
                {t('confirm', 'Confirm')}
              </button>
            </div>
          </>
        )}
      </ModalShell>
    </div>
  );
}
