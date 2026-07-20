import { useEffect } from 'react';
import axios from 'axios';
import { driver } from 'driver.js';
import 'driver.js/dist/driver.css';
import { useI18n } from '../contexts/I18nContext';

export default function OnboardingTour({ user }: { user: any }) {
  const { t } = useI18n();

  useEffect(() => {
    const startTour = async (isRerun: boolean) => {
      // Report to telemetry that tour is starting
      try {
        await axios.post('/api/me/onboarding', {
          status: 'in_progress',
          last_step: 'welcome',
          is_rerun: isRerun
        });
      } catch (e) {
        console.error("Failed to report onboarding start telemetry", e);
      }

      const driverObj = driver({
        showProgress: true,
        allowClose: true,
        steps: [
          {
            element: '.sidebar',
            popover: {
              title: t('tour_sidebar_title', 'Sidebar Navigation'),
              description: t('tour_sidebar_desc', 'Use the sidebar to navigate between your Dashboard Overview, Administration features, and System Settings.'),
              side: 'right',
              align: 'start'
            }
          },
          {
            element: '#dashboard-overview',
            popover: {
              title: t('tour_header_title', 'Dashboard Overview'),
              description: t('tour_header_desc', 'This is your central control panel where you can manage your tunnels and domains.'),
              side: 'bottom',
              align: 'start'
            }
          },
          {
            element: '#tour-tunnels-panel',
            popover: {
              title: t('tour_end_title', 'Active Tunnels'),
              description: t('tour_end_desc', 'See a real-time list of all your active CLI connections here. You are all set!'),
              side: 'top',
              align: 'center'
            }
          }
        ],
        onDestroyed: () => {
          const status = 'completed';
          axios.post('/api/me/onboarding', {
            status: status,
            last_step: 'completed',
            is_rerun: false
          }).catch(err => console.error("Failed to report onboarding completion", err));
        }
      });

      driverObj.drive();
    };

    // Auto-start if pending
    if (user && user.onboarding_status === 'pending') {
      const timer = setTimeout(() => startTour(false), 1200);
      return () => clearTimeout(timer);
    }

    // Listen for manual trigger
    const handleStartTour = () => startTour(true);
    window.addEventListener('start-onboarding-tour', handleStartTour as EventListener);

    return () => {
      window.removeEventListener('start-onboarding-tour', handleStartTour as EventListener);
    };
  }, [user, t]);

  return null; // This is a headless component
}
