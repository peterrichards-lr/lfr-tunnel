import { useEffect, useState } from 'react';
import axios from 'axios';
import { useI18n } from '../contexts/I18nContext';
import { useUI } from '../contexts/UIContext';

interface EdgeScheduleModalProps {
  nodeId: string;
  onClose: () => void;
  onSaved: () => void;
}

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

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await axios.get(`/api/admin/edge/${encodeURIComponent(nodeId)}/schedule`);
        if (cancelled) return;
        setStopTime(res.data.stop_time || '');
        setStartTime(res.data.start_time || '');
        setTimezone(res.data.timezone || '');
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
        enabled: true,
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
        className="modal-card modal-card--md p-xl"
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
              <label className="form-label text-xs">{t('edge_schedule_stop_time', 'Stop time (local)')}</label>
              <input type="time" className="input-field" value={stopTime} onChange={e => setStopTime(e.target.value)} />
            </div>
            <div className="form-group m-0">
              <label className="form-label text-xs">{t('edge_schedule_start_time', 'Start time (local)')}</label>
              <input type="time" className="input-field" value={startTime} onChange={e => setStartTime(e.target.value)} />
            </div>
            <div className="form-group m-0">
              <label className="form-label text-xs">{t('edge_schedule_timezone', 'Timezone (IANA)')}</label>
              <input
                type="text"
                className="input-field"
                placeholder="e.g. America/Sao_Paulo"
                value={timezone}
                onChange={e => setTimezone(e.target.value)}
              />
            </div>

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
