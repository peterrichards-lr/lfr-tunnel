import { useState, useEffect } from 'react';
import { useI18n } from '../contexts/I18nContext';

export default function ClientInstallationModal({ isOpen, onClose, serverConfig }: { isOpen: boolean, onClose: () => void, serverConfig?: any }) {
  const [activeTab, setActiveTab] = useState<'macos' | 'windows' | 'linux'>('macos');
  const [copied, setCopied] = useState<string | null>(null);
  const { t } = useI18n();

  useEffect(() => {
    if (isOpen) {
      document.body.style.overflow = 'hidden';
      const ua = navigator.userAgent;
      if (ua.includes('Windows')) {
        setActiveTab('windows');
      } else if (ua.includes('Linux')) {
        setActiveTab('linux');
      } else {
        setActiveTab('macos');
      }
    } else {
      document.body.style.overflow = 'unset';
    }
    return () => { document.body.style.overflow = 'unset'; };
  }, [isOpen]);

  const handleCopy = (text: string, id: string) => {
    navigator.clipboard.writeText(text);
    setCopied(id);
    setTimeout(() => setCopied(null), 2000);
  };

  if (!isOpen) return null;

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal-card modal-card--md max-h-[90vh] flex flex-col p-xl" onClick={e => e.stopPropagation()}>
        
        {/* Header */}
        <div className="modal-header mb-md">
          <h2 className="modal-title text-md">{t('guide_title', 'Client Installation Guide')}</h2>
          <button onClick={onClose} className="modal-close">×</button>
        </div>
        
        <div className="text-xs text-muted mb-xl">
          {t('guide_desc', 'Choose your Operating System below to see the recommended command-line installation or direct downloads.')}
        </div>

        {/* Tab Headers */}
        <div className="sub-tabs mb-xl">
          {(['macos', 'windows', 'linux'] as const).map(os => (
            <button 
              key={os}
              onClick={() => setActiveTab(os)}
              className={`sub-tab ${activeTab === os ? 'sub-tab--active' : ''}`}
            >
              {t(`guide_tab_${os}`, os === 'macos' ? 'macOS' : os === 'windows' ? 'Windows' : 'Linux')}
            </button>
          ))}
        </div>

        {/* Tab Contents */}
        <div className="flex-1 overflow-y-auto pr-xs mb-xl">
          {/* macOS */}
          {activeTab === 'macos' && (
            <div className="animation-fade-in">
              <h4 className="text-xs fw-bold mb-xs">🚀 {t('guide_macos_title', 'Apple Silicon (M1/M2/M3) & Intel Macs')}</h4>
              
              {!serverConfig?.disable_brew && (
                <>
                  <div className="text-2xs fw-bold mt-sm text-main">
                    {t('guide_macos_brew', 'Recommended via Homebrew:')}
                  </div>
                  <div className="code-box">
                    <span>brew tap peterrichards-lr/homebrew-tap && brew install lfr-tunnel</span>
                    <button className="copy-btn" onClick={() => handleCopy('brew tap peterrichards-lr/homebrew-tap && brew install lfr-tunnel', 'macos-brew')}>
                      {copied === 'macos-brew' ? '✓' : '📋'}
                    </button>
                  </div>
                </>
              )}

              <div className={`text-2xs fw-bold mt-sm ${!serverConfig?.disable_brew ? 'text-muted' : 'text-main'}`}>
                {t('guide_macos_direct', 'Direct Installation Script (Alternative):')}
              </div>
              <div className="code-box">
                <span>curl -fsSL https://tunnel.lfr-demo.se/install | sh</span>
                <button className="copy-btn" onClick={() => handleCopy('curl -fsSL https://tunnel.lfr-demo.se/install | sh', 'macos-direct')}>
                  {copied === 'macos-direct' ? '✓' : '📋'}
                </button>
              </div>
              
              <div className="mt-lg flex gap-md flex-wrap">
                <a href="/static/downloads/lfr-tunnel-darwin-arm64" className="btn btn-outline py-xs px-md text-xs w-auto m-0">
                  {t('guide_macos_dl_arm', 'Download arm64 (M1/M2/M3)')}
                </a>
                <a href="/static/downloads/lfr-tunnel-darwin-amd64" className="btn btn-outline py-xs px-md text-xs w-auto m-0">
                  {t('guide_macos_dl_intel', 'Download amd64 (Intel)')}
                </a>
              </div>
            </div>
          )}

          {/* Windows */}
          {activeTab === 'windows' && (
            <div className="animation-fade-in">
              <h4 className="text-xs fw-bold mb-xs">🚀 {t('guide_windows_title', 'Windows 10 / 11 (64-bit)')}</h4>
              
              {!serverConfig?.disable_scoop && (
                <>
                  <div className="text-2xs fw-bold mt-sm text-main">
                    {t('guide_windows_scoop', 'Recommended via Scoop:')}
                  </div>
                  <div className="code-box">
                    <span>scoop bucket add peterrichards-lr https://github.com/peterrichards-lr/scoop-bucket.git && scoop install lfr-tunnel</span>
                    <button className="copy-btn" onClick={() => handleCopy('scoop bucket add peterrichards-lr https://github.com/peterrichards-lr/scoop-bucket.git && scoop install lfr-tunnel', 'win-scoop')}>
                      {copied === 'win-scoop' ? '✓' : '📋'}
                    </button>
                  </div>
                </>
              )}

              <div className={`text-2xs fw-bold mt-sm ${!serverConfig?.disable_scoop ? 'text-muted' : 'text-main'}`}>
                {t('guide_windows_direct', 'Direct Installation (PowerShell Script):')}
              </div>
              <div className="code-box">
                <span>irm https://tunnel.lfr-demo.se/install.ps1 | iex</span>
                <button className="copy-btn" onClick={() => handleCopy('irm https://tunnel.lfr-demo.se/install.ps1 | iex', 'win-direct')}>
                  {copied === 'win-direct' ? '✓' : '📋'}
                </button>
              </div>
              
              <div className="mt-lg flex gap-md">
                <a href="/static/downloads/lfr-tunnel-windows-amd64.exe" className="btn btn-outline py-xs px-md text-xs w-auto m-0">
                  {t('guide_windows_dl', 'Download amd64 (.exe)')}
                </a>
              </div>
            </div>
          )}

          {/* Linux */}
          {activeTab === 'linux' && (
            <div className="animation-fade-in">
              <h4 className="text-xs fw-bold mb-xs">🚀 {t('guide_linux_title', 'Linux (amd64 / arm64)')}</h4>
              
              <div className="text-2xs fw-bold mt-sm text-main">
                {t('guide_linux_direct', 'Direct Installation Script:')}
              </div>
              <div className="code-box">
                <span>curl -fsSL https://tunnel.lfr-demo.se/install | sh</span>
                <button className="copy-btn" onClick={() => handleCopy('curl -fsSL https://tunnel.lfr-demo.se/install | sh', 'linux-direct')}>
                  {copied === 'linux-direct' ? '✓' : '📋'}
                </button>
              </div>
              
              <div className="mt-lg flex gap-md flex-wrap">
                <a href="/static/downloads/lfr-tunnel-linux-amd64" className="btn btn-outline py-xs px-md text-xs w-auto m-0">
                  {t('guide_linux_dl_amd', 'Download amd64')}
                </a>
                <a href="/static/downloads/lfr-tunnel-linux-arm64" className="btn btn-outline py-xs px-md text-xs w-auto m-0">
                  {t('guide_linux_dl_arm', 'Download arm64')}
                </a>
              </div>
            </div>
          )}
        </div>

        <style>{`
          .code-box {
            margin-top: 6px;
            margin-bottom: 14px;
            position: relative;
            background: #0d1117;
            color: #e6edf3;
            border-radius: 6px;
            border: 1px solid rgba(255,255,255,0.15);
            padding: 14px 48px 14px 24px;
            font-family: monospace;
            font-size: 0.82rem;
            word-break: break-all;
            line-height: 1.5;
          }
          .copy-btn {
            position: absolute;
            top: 6px;
            right: 6px;
            background: transparent;
            border: 1px solid rgba(255,255,255,0.2);
            color: #8b949e;
            border-radius: 4px;
            width: 22px;
            height: 22px;
            cursor: pointer;
            display: flex;
            align-items: center;
            justify-content: center;
            font-size: 10px;
          }
          .copy-btn:hover {
            background: rgba(255,255,255,0.1);
          }
          .animation-fade-in {
            animation: fadeIn 0.3s ease-out;
          }
          @keyframes fadeIn {
            from { opacity: 0; transform: translateY(5px); }
            to { opacity: 1; transform: translateY(0); }
          }
        `}</style>
      </div>
    </div>
  );
}
