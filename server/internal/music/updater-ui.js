// Updates survive a service restart; poll again after reconnecting or signing in.
(() => {
  const panel = $('updateStatus').closest('.panel');
  const actions = document.createElement('div');
  actions.className = 'settings-actions';
  const install = document.createElement('button');
  install.id = 'installUpdate';
  install.className = 'btn primary';
  install.textContent = '安装更新并重启';
  install.hidden = true;
  const notes = document.createElement('pre');
  notes.id = 'updateNotes';
  notes.style.cssText = 'white-space:pre-wrap;font:inherit;overflow-wrap:anywhere';
  actions.append(install);
  const releaseLink=document.createElement('a');
  releaseLink.className='btn';releaseLink.textContent='查看发行页面';releaseLink.target='_blank';releaseLink.rel='noopener noreferrer';releaseLink.hidden=true;
  actions.append(releaseLink);
  panel.append(notes, actions);
  let selected = null;
  const busy = new Set(['checking', 'queued', 'downloading', 'stopping', 'backing_up', 'switching', 'starting', 'verifying', 'rolling_back', 'recovery_failed']);
  loadUpdate = async function () {
    try {
      const s = await api('admin/update');
      selected = s;
      const names = {disabled:'未配置更新仓库',checking:'正在检查',current:'当前已是最新版本',available:'发现新版本',incompatible:'当前更新路径不兼容',queued:'更新已排队',downloading:'下载并验证签名',stopping:'正在停止服务',backing_up:'正在备份数据库',switching:'正在切换程序',starting:'正在启动服务',verifying:'正在检查新版本',completed:'更新成功',rolled_back:'更新失败，已恢复原版本',failed:'更新失败，原版本保留',recovery_failed:'恢复失败，需要管理员处理',error:'检查失败'};
      $('updateStatus').textContent = `${names[s.state] || s.state} · 当前 ${s.current_version}${s.latest_version ? ' · 最新 ' + s.latest_version : ''}${s.message ? ' · ' + s.message : ''}`;
      $('updatePackage').textContent = [s.schema ? `数据库 schema ${s.schema}` : '', s.commit ? `提交 ${s.commit.slice(0, 12)}` : '', s.backup_path ? '备份：' + s.backup_path : '', !s.can_install ? '此环境支持检查更新；自动安装需在 Ubuntu 配置独立更新服务。' : '安装期间会短暂停机，完成后可能需要重新登录。'].filter(Boolean).join(' · ');
      notes.textContent = s.notes || '';
      releaseLink.hidden=true;
      try {const url=new URL(s.release_url);if(url.protocol==='https:'&&url.hostname==='github.com'){releaseLink.href=url.href;releaseLink.hidden=false;}}catch{}
      install.hidden = !(s.state === 'available' && s.compatible && s.can_install);
      install.disabled = busy.has(s.state);
      $('checkUpdate').disabled = busy.has(s.state);
      return s;
    } catch (error) {
      $('updateStatus').textContent = '服务暂时不可用或登录已过期；更新期间请稍候，恢复后重新登录查看结果。';
      throw error;
    }
  };
  install.onclick = () => run(async () => {
    const s = selected;
    if (!s || !s.compatible || !s.can_install) return;
    if (!confirm(`安装 ${s.latest_version} 并重新启动服务器？\n安装前会备份数据库，启动失败会回退。播放会短暂中断，完成后可能需要重新登录。`)) return;
    install.disabled = true;
    try { await api('admin/update/apply', 'POST', {version:s.latest_version}); }
    finally { await loadUpdate(); }
  });
})();
