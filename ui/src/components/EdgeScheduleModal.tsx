import { useEffect, useState } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

interface EdgeScheduleModalProps {
  nodeId: string;
  onClose: () => void;
  onSaved: () => void;
}

function formatTimezoneOffset(zone: string): string {
  try {
    const parts = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'shortOffset' }).formatToParts(new Date());
    const offset = parts.find(p => p.type === 'timeZoneName');
    return offset ? offset.value : 'UTC';
  } catch {
    return '';
  }
}

// Full list of valid IANA timezone identifiers, grouped by geographic region
// (the part before the "/") so a ~400-entry list is actually browsable, each
// labelled with its current UTC offset -- that offset is what would have
// caught the edge-sa/edge-in misconfiguration at a glance (both were left on
// "Europe/London", a real but wrong timezone; free text let that
// typo-shaped mistake through silently). Computed once at module load since
// the list is static for the process lifetime.
const TIMEZONE_GROUPS: [string, string[]][] = (() => {
  let zones: string[];
  try {
    zones = Intl.supportedValuesOf('timeZone');
  } catch {
    // Intl.supportedValuesOf isn't available in every browser -- fall back
    // to just the regions this project actually uses rather than an empty
    // dropdown.
    zones = ['UTC', 'America/New_York', 'America/Sao_Paulo', 'Europe/London', 'Europe/Dublin', 'Asia/Tokyo', 'Asia/Kolkata'];
  }
  const groups: Record<string, string[]> = {};
  zones.forEach(zone => {
    const region = zone.includes('/') ? zone.split('/')[0] : 'Other';
    (groups[region] = groups[region] || []).push(zone);
  });
  return Object.keys(groups).sort().map(region => [region, groups[region].sort()]);
})();

// Edits an edge node's EventBridge Scheduler stop/start window via
// GET/PUT /api/admin/edge/{id}/schedule (see issues #885, #888). This
// updates the existing schedule in place -- the backend/sidecar never
// delete-and-recreates it, so repeated edits are cheap and don't lose
// history a user might reasonably expect to still exist elsewhere.
export default function EdgeScheduleModal({ nodeId, onClose, onSaved }: EdgeScheduleModalProps) {
  const { t } = useI18n();
  const { showToast } = useUI();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [stopTime, setStopTime] = useState('');
  const [startTime, setStartTime] = useState('');
  const [timezone, setTimezone] = useState('');
  const [enabled, setEnabled] = useState(true);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await axios.get(`/api/admin/edge/${encodeURIComponent(nodeId)}/schedule`);
        if (cancelled) return;
        setStopTime(res.data.stop_time || '');
        setStartTime(res.data.start_time || '');
        setTimezone(res.data.timezone || '');
        setEnabled(res.data.enabled !== false);
      } catch (e: any) {
        if (e.response?.status !== 404) {
          showToast(e.response?.data?.error || 'Failed to load schedule.', 'error');
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodeId]);

  const handleSave = async () => {
    if (!stopTime || !startTime || !timezone.trim()) {
      showToast('Stop time, start time, and timezone are all required.', 'error');
      return;
    }
    setSaving(true);
    try {
      await axios.put(`/api/admin/edge/${encodeURIComponent(nodeId)}/schedule`, {
        enabled,
        stop_time: stopTime,
        start_time: startTime,
        timezone: timezone.trim(),
      });
      showToast('Schedule updated.', 'success');
      onSaved();
      onClose();
    } catch (e: any) {
      showToast(e.response?.data?.error || 'Failed to update schedule.', 'error');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div
        className="modal-card modal-card--md"
        onClick={e => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-labelledby="edge-schedule-modal-title"
      >
        <div className="modal-header mb-md">
          <h2 id="edge-schedule-modal-title" className="modal-title text-md">
            {t('edge_schedule_modal_title', 'Edit Stop/Start Schedule')}
          </h2>
          <button type="button" onClick={onClose} className="modal-close" aria-label={t('close', 'Close')}>×</button>
        </div>

        <p className="text-xs text-muted mb-md">{nodeId}</p>

        {loading ? (
          <p className="text-xs text-muted">{t('loading', 'Loading...')}</p>
        ) : (
          <>
            <div className="form-group m-0">
              <label className="form-label text-xs" htmlFor="edge-schedule-stop-time">{t('edge_schedule_stop_time', 'Stop time (local)')}</label>
              <input id="edge-schedule-stop-time" type="time" className="input-field" value={stopTime} onChange={e => setStopTime(e.target.value)} />
            </div>
            <div className="form-group m-0">
              <label className="form-label text-xs" htmlFor="edge-schedule-start-time">{t('edge_schedule_start_time', 'Start time (local)')}</label>
              <input id="edge-schedule-start-time" type="time" className="input-field" value={startTime} onChange={e => setStartTime(e.target.value)} />
            </div>
            <div className="form-group m-0">
              <label className="form-label text-xs" htmlFor="edge-schedule-timezone">{t('edge_schedule_timezone', 'Timezone')}</label>
              <select id="edge-schedule-timezone"
                className="input-field"
                value={timezone}
                onChange={e => setTimezone(e.target.value)}
              >
                <option value="">-- Select timezone --</option>
                {TIMEZONE_GROUPS.map(([region, zones]) => (
                  <optgroup key={region} label={region}>
                    {zones.map(zone => (
                      <option key={zone} value={zone}>{zone} ({formatTimezoneOffset(zone)})</option>
                    ))}
                  </optgroup>
                ))}
              </select>
            </div>

            <label className="flex items-center gap-sm mt-md text-sm opacity-80">
              <input type="checkbox" checked={enabled} onChange={e => setEnabled(e.target.checked)} />
              {t('edge_schedule_enabled', 'Schedule enabled for this node')}
            </label>
            <p className="text-2xs text-muted mt-xs">
              {t('edge_schedule_disabled_hint', "Unchecking this pauses the stop/start schedule for this node only, without affecting other nodes -- it stays under manual control (see the Start/Stop/Restart actions) until re-enabled.")}
            </p>

            <div className="flex justify-end gap-sm mt-lg">
              <button type="button" className="btn btn-secondary" onClick={onClose} disabled={saving}>
                {t('cancel', 'Cancel')}
              </button>
              <button type="button" className="btn btn-primary" onClick={handleSave} disabled={saving}>
                {saving ? t('saving', 'Saving...') : t('save', 'Save')}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
