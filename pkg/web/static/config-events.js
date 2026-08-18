(() => {
  const mode = document.body.dataset.configWatch;
  if (!mode || !window.EventSource) return;

  const stream = new EventSource('/config/stream');
  stream.addEventListener('auth-changed', () => {
    window.location.assign('/login?configChanged=1');
  });
  stream.addEventListener('config-changed', () => {
    if (mode === 'reload') {
      window.location.reload();
      return;
    }
    if (mode !== 'warn' || document.querySelector('[data-config-change-notice]')) return;

    const notice = document.createElement('div');
    notice.className = 'notice config-change-notice';
    notice.dataset.configChangeNotice = 'true';
    notice.setAttribute('role', 'status');
    notice.append('配置已被外部修改。为避免覆盖新配置，请刷新页面后继续。');

    const refresh = document.createElement('button');
    refresh.className = 'link';
    refresh.type = 'button';
    refresh.textContent = '立即刷新';
    refresh.addEventListener('click', () => window.location.reload());
    notice.append(refresh);

    const main = document.querySelector('main');
    if (main) main.prepend(notice);
  });
})();
