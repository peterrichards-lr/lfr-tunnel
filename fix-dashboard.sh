#!/bin/bash
sed -i.bak "s/window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', e => {/const mq = window.matchMedia('(prefers-color-scheme: dark)'); if (mq) { const handler = e => {/g" pkg/client/dashboard.html
sed -i.bak "s/if (currentTheme === 'system') applyTheme('system');/if (currentTheme === 'system') applyTheme('system'); }; if (mq.addEventListener) mq.addEventListener('change', handler); else if (mq.addListener) mq.addListener(handler);/g" pkg/client/dashboard.html

sed -i.bak "s/themeBtn.addEventListener('click', () => {/if (themeBtn) themeBtn.addEventListener('click', () => {/g" pkg/client/dashboard.html
sed -i.bak "s/autoScrollCheckbox.addEventListener('change', (e) => {/if (autoScrollCheckbox) autoScrollCheckbox.addEventListener('change', (e) => {/g" pkg/client/dashboard.html
sed -i.bak "s/logsContainer.addEventListener('scroll', () => {/if (logsContainer) logsContainer.addEventListener('scroll', () => {/g" pkg/client/dashboard.html
sed -i.bak "s/filterSearch.addEventListener('input', renderLogs);/if (filterSearch) filterSearch.addEventListener('input', renderLogs);/g" pkg/client/dashboard.html
sed -i.bak "s/filterLevel.addEventListener('change', renderLogs);/if (filterLevel) filterLevel.addEventListener('change', renderLogs);/g" pkg/client/dashboard.html
sed -i.bak "s/document.getElementById('btn-refresh').addEventListener('click', fetchLogs);/const btnRefresh = document.getElementById('btn-refresh'); if (btnRefresh) btnRefresh.addEventListener('click', fetchLogs);/g" pkg/client/dashboard.html
sed -i.bak "s/document.getElementById('btn-clear').addEventListener('click', () => {/const btnClear = document.getElementById('btn-clear'); if (btnClear) btnClear.addEventListener('click', () => {/g" pkg/client/dashboard.html
