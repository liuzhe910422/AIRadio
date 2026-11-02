const $ = (selector) => document.querySelector(selector);
let heroTalking = false;
$('#hero-talk').addEventListener('click', () => {
  heroTalking = !heroTalking;
  $('#hero-talk').classList.toggle('is-talking', heroTalking);
  $('#hero-talk').setAttribute('aria-label', heroTalking ? '体验结束对讲' : '体验开始对讲');
  $('#hero-talk span').textContent = heroTalking ? '点击结束' : '点击开讲';
  $('#hero-device-status').textContent = heroTalking ? '演示：你正在发言，点击按钮结束' : '说「你好，小智」唤醒语音助手';
});
const demo = $('#demo-content');
let mode = 'talk';
let talking = false;
let hosted = true;
let room = '物流一组';
const templates = {
  talk: () => `<div class="demo-room">${room}<small>普通模式 · ROOM ${room === '物流一组' ? '100' : '208'}</small></div><div class="demo-talk-layout"><button class="demo-talk-button ${talking ? 'recording' : ''}" id="demo-talk" aria-pressed="${talking}"><span class="mic-symbol" aria-hidden="true">${talking ? '▥' : '◉'}</span>${talking ? '结束对讲' : '开始对讲'}</button><div class="demo-info"><b>${talking ? '你正在发言…' : '沟通，从一次点击开始。'}</b><p>${talking ? '话语正在传递给同房间伙伴。<br>再次点击，即可结束本次发言。' : '无需唤醒，点击大按钮即可开讲。<br>发言状态清晰呈现，开始结束由你掌握。'}</p></div></div><div class="demo-event">${talking ? '▂ ▅ █ ▃ ▆ ▂ ▅　正在演示语音发送' : '● 团队已连接 · 点击左侧大按钮体验'}</div>`,
  voice: () => `<div class="command-bubble"><span>✳</span> “你好，小智，帮我查看房间列表。”</div><div class="room-options"><button class="room-option ${room === '物流一组' ? 'selected' : ''}" data-room="物流一组">物流一组 ${room === '物流一组' ? '✓' : ''}<small>加入码 1100 · 场景演示</small></button><button class="room-option ${room === '仓储协作组' ? 'selected' : ''}" data-room="仓储协作组">仓储协作组 ${room === '仓储协作组' ? '✓' : ''}<small>加入码 1208 · 场景演示</small></button></div><div class="demo-event">${room === '仓储协作组' ? '✓ 演示：已切换至仓储协作组' : '语音发起查询，再点击房间确认选择。试试切换协作组。'}</div><p class="demo-caption">也可通过语音创建房间、接受最近邀请、开启或关闭托管。</p>`,
  hosting: () => `<div class="hosting-demo-head"><span>✳ ${hosted ? '在线托管已开启' : '当前为普通模式'}</span><button class="toggle" role="switch" aria-checked="${hosted}" aria-label="开启 AI 托管演示" id="hosting-toggle"><i></i></button></div><div class="dialog-message"><small>004 · 团队成员</small>002，在吗？</div><div class="dialog-message ai"><small>${hosted ? '✳ 002 · AI 托管回复' : '002 · 等待真人回应'}</small>${hosted ? '002 的 AI 助手在线，请讲。' : '开启右上角开关，体验 AI 协助代答。'}</div><p class="demo-caption">结合近期对话判断是否回复 · 真人发言可打断自动语音</p><div class="demo-event">${hosted ? '理解对话 → 判断是否代答 → 合成语音回应' : '普通模式下，由你亲自发言。'}</div>`,
  recall: () => `<div class="command-bubble"><span>✳</span> “总结一下刚才大家聊了什么。”</div><div class="summary-box"><b>AI / 近期对讲回顾 · 演示内容</b>004 通知装货区已准备就绪，可以进场。<br>你回复已收到，正在前往装货区。</div><div class="demo-event">语音转写 → 近期记录 → 重点总结</div><p class="demo-caption">总结基于当前保留且已完成识别的文字，不代表完整历史。</p>`
};
function renderDemo() {
  demo.innerHTML = templates[mode]();
  demo.style.animation = 'none';
  void demo.offsetWidth;
  demo.style.animation = '';
  $('#demo-talk')?.addEventListener('click', () => { talking = !talking; renderDemo(); $('#demo-talk').focus({ preventScroll: true }); });
  $('#hosting-toggle')?.addEventListener('click', () => { hosted = !hosted; renderDemo(); $('#hosting-toggle').focus({ preventScroll: true }); });
  document.querySelectorAll('[data-room]').forEach(button => button.addEventListener('click', () => { room = button.dataset.room; renderDemo(); document.querySelector(`[data-room="${room}"]`).focus({ preventScroll: true }); }));
}
const tabs = [...document.querySelectorAll('.experience-tab')];
function selectTab(tab) {
  mode = tab.dataset.mode;
  talking = false;
  tabs.forEach(button => { const selected = button === tab; button.classList.toggle('active', selected); button.setAttribute('aria-selected', selected); button.tabIndex = selected ? 0 : -1; });
  $('#demo-panel').setAttribute('aria-labelledby', tab.id);
  renderDemo();
}
tabs.forEach((tab, index) => {
  tab.addEventListener('click', () => selectTab(tab));
  tab.addEventListener('keydown', event => {
    let next;
    if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = (index + 1) % tabs.length;
    if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = (index + tabs.length - 1) % tabs.length;
    if (event.key === 'Home') next = 0;
    if (event.key === 'End') next = tabs.length - 1;
    if (next !== undefined) { event.preventDefault(); selectTab(tabs[next]); tabs[next].focus(); }
  });
});
renderDemo();
const photos = {
  idle: ['大按钮，让开讲更直接。', '房间、设备、对讲状态清晰呈现。左侧即时发言，右侧回看消息，让常用操作触手可及。', '实机普通模式，蓝色大按钮和右侧对讲记录'],
  speaking: ['谁在说话，一眼就知道。', '当前发言设备与说话状态直接显示，帮助团队成员掌握沟通节奏。', '实机正在接收设备 004 发言，显示说话中状态'],
  transcript: ['声音之外，还有文字。', '截图中保留了设备 004 的 7 秒语音记录和识别文字，可点击记录回放原音，方便核对近期沟通内容。', '实机显示设备 004 的 7 秒语音记录与识别文字'],
  hosting: ['模式变化，清楚可见。', '开启在线托管后，主按钮变为黄色。AI 按需协助回应，也可通过语音或点击请求退出。', '实机黄色托管模式按钮'],
  rooms: ['说出需求，选择协作空间。', '通过语音唤起房间列表，再触摸选择目标房间。让房间管理更直观，减少逐层寻找菜单。', '实机语音触发的房间选择列表']
};
let currentPhoto = 'idle';
document.querySelectorAll('[data-photo]').forEach(button => button.addEventListener('click', () => {
  currentPhoto = button.dataset.photo;
  const [title, description, alt] = photos[currentPhoto];
  $('#real-image').src = `assets/device-${currentPhoto}.webp`;
  $('#real-image').alt = alt;
  $('#real-title').textContent = title;
  $('#real-description').textContent = description;
  document.querySelectorAll('[data-photo]').forEach(item => { const selected = item === button; item.classList.toggle('selected', selected); item.setAttribute('aria-pressed', selected); });
}));
const photoDialog = $('#photo-dialog');
$('#open-photo').addEventListener('click', () => {
  photoDialog.querySelector('img').src = $('#real-image').src;
  photoDialog.querySelector('img').alt = $('#real-image').alt;
  $('#photo-caption').textContent = photos[currentPhoto][0];
  photoDialog.showModal();
});
$('.dialog-close').addEventListener('click', () => photoDialog.close());
photoDialog.addEventListener('click', event => { if (event.target === photoDialog) { const rect = photoDialog.getBoundingClientRect(); if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) photoDialog.close(); } });
if ('IntersectionObserver' in window && !window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
  const observer = new IntersectionObserver(entries => entries.forEach(entry => { if (entry.isIntersecting) { entry.target.classList.add('visible'); observer.unobserve(entry.target); } }), { threshold: .08 });
  document.querySelectorAll('.section-heading, .feature-card, .scenario, .reality-layout').forEach(element => { element.classList.add('reveal'); observer.observe(element); });
}

// Product scenarios are local illustrations; no device or AI requests are made.
let translated = false;
let sharedImage = false;
let taskState = 'draft';
templates.translation = () => `<div class="demo-room">同一现场，不同语言<small>001 中文 → 002 English</small></div><div class="message-card"><span class="message-label">001 · 原始对讲</span><p>装货区已准备就绪，请前往三号门。</p><span class="audio-line">▂ ▅ ▃ █ ▂ ▅ <small>原始语音保留</small></span></div>${translated ? '<div class="message-card translated"><span class="message-label">002 · 英文设备收到翻译</span><p>The loading area is ready. Please proceed to Gate 3.</p><span class="audio-line">▂ ▃ ▅ ▂ █ ▃ <small>译文 + 英文合成音频</small></span></div>' : '<div class="translation-route"><span>原始对讲</span><i>→</i><span>语音识别</span><i>→</i><span>英文翻译与合成</span></div>'}<button class="demo-action" id="translate-demo">${translated ? '重置翻译演示 ↺' : '查看英文设备收到的内容 →'}</button><button class="translation-proof" data-proof="translation" aria-label="放大查看英文翻译实机截图"><img src="assets/device-translation.webp" alt="英文设备同时显示中文原话与 Translation 英文译文" loading="lazy"><span>查看真实英文界面 <i>↗</i></span></button><p class="demo-caption">以上对话为场景模拟，无音频播放；实拍图片为设备翻译记录。英文翻译异步生成，在原始音频播放完成后排队播放。</p>`;
templates.image = () => `<div class="command-bubble"><span>◎</span> “拍张照片，给大家看看现场。”</div><div class="image-demo"><button class="image-preview" aria-label="放大查看图片协作实机截图"><img src="assets/device-image.webp" alt="视频中实机打开接收图片的大图预览" loading="lazy"><span>图片预览 · 实机截图 ↗</span></button><div class="image-delivery"><span class="message-label">002 → 当前房间 → 001</span><h3>${sharedImage ? '现场图片，团队可见。' : '让现场信息更直观。'}</h3><p>${sharedImage ? '001 收到 002 分享的图片，可在对讲记录中查看缩略图、打开大图。' : '拍照之后，将图片分享给同房间成员，为沟通补充视觉信息。'}</p><button class="demo-action" id="share-demo">${sharedImage ? '重新演示 ↺' : '模拟发送到房间 →'}</button></div></div><div class="demo-event">${sharedImage ? '✓ 图片已送达 · 可点击实机截图放大查看' : '拍照 → 上传图片 → 房间广播 → 成员查看'}</div><p class="demo-caption">示意流程；拍照需摄像头支持。图片暂存服务端内存，重启后失效。</p>`;
templates.task = () => `<div class="command-bubble"><span>↗</span> “提醒 002，今天下午三点检查装货区。”</div><div class="task-demo-card"><div class="task-card-head"><span>002 / 现场任务</span><span class="task-state ${taskState === 'completed' ? 'completed' : ''}">${{draft:'待创建',pending:'待处理',reminded:'已提醒',completed:'已完成'}[taskState]}</span></div><h3>检查装货区</h3><p>今天 15:00 · 由设备 001 派发</p><div class="task-progress"><span class="${taskState !== 'draft' ? 'done' : ''}">创建</span><i></i><span class="${['reminded','completed'].includes(taskState) ? 'done' : ''}">提醒</span><i></i><span class="${taskState === 'completed' ? 'done' : ''}">完成</span></div></div><button class="demo-action" id="task-demo">${{draft:'模拟创建任务 →',pending:'查看提醒 →',reminded:'模拟人员确认完成 ✓',completed:'重新演示 ↺'}[taskState]}</button><p class="demo-caption">定时事件支持创建、查询、完成与取消。完成状态由人员确认，不代表设备代为执行现场作业。</p>`;
// Delegate actions so re-rendered controls retain keyboard focus.
demo.addEventListener('click', event => {
  const button = event.target.closest('button');
  if (!button) return;
  const id = button.id;
  if (id === 'translate-demo') translated = !translated;
  else if (id === 'share-demo') sharedImage = !sharedImage;
  else if (id === 'task-demo') taskState = ({draft:'pending',pending:'reminded',reminded:'completed',completed:'draft'})[taskState];
  else return;
  renderDemo();
  document.getElementById(id)?.focus({ preventScroll: true });
});
document.querySelectorAll('[data-demo-link]').forEach(link => link.addEventListener('click', () => {
  selectTab(document.querySelector(`[data-mode="${link.dataset.demoLink}"]`));
}));
photos.translation = ['原话与译文，在同一屏呈现。', '来自翻译功能演示的实机截图：002 使用英文界面，同时保留 001 的中文原话和带 Translation 标记的英文译文，记录可点击回放。', '英文实机界面显示中文原话，以及 Translation 和 Hello, hello, hello, what are you doing? 英文译文'];
photos.image = ['现场照片，在同一房间共享。', '来自拍照功能演示的实机截图：001 接收 002 分享的图片后打开大图，团队可以查看对方分享的现场信息。', '实机显示房间中收到的照片，右上角可关闭大图'];
photos.task = ['任务进度，与团队沟通同屏。', '来自告警 Agent 演示的实机截图：002 收到更换传感器的处理任务，上方显示待处理事件数量。', '实机收到更换传感器的红色事件通知，并显示一条待处理事件'];
const workflowStates = [
  {tag:'SENSOR / 现场感知', title:'异常，让团队及时知道。', copy:'001 的温度超过配置阈值，服务端启动已配置的高温处理流程。', visual:'<div class="sensor-reading"><span>001 / 当前温度</span><strong>38.6<small>°C</small></strong><div><span class="warning-dot"></span> 高于演示阈值 35°C</div><svg viewBox="0 0 360 70" aria-hidden="true"><path d="M0 55H360" class="chart-line"/><path d="M0 62L30 58L60 60L90 49L120 51L150 36L180 40L210 25L240 31L270 13L300 20L330 8L360 5"/></svg></div>', note:'周期采集 · 服务端阈值判断'},
  {tag:'CAPTURE / 补充信息', title:'拍照任务，送到现场人员。', copy:'001 收到拍照待办。人员说出拍照指令，图片上传后关联当前任务，进入视觉判断。', visual:'<div class="flow-task"><span class="message-label">任务 → 001</span><h3>请拍摄设备状态照片</h3><p>“你好，小智，拍张照片。”</p><div class="flow-mini-route"><span>语音拍照</span><i>→</i><span>上传图片</span><i>→</i><span>关联任务</span></div></div>', note:'由人员发起拍照 · 需摄像头支持'},
  {tag:'VISION / 辅助判断', title:'看见异常，再组织处理。', copy:'视觉模型根据配置的提示词检查照片中的红色指示灯，识别到异常后推进人工处理步骤。', visual:'<div class="vision-demo"><div class="vision-target" aria-hidden="true"><div class="machine-led"></div><span>STATUS</span><div class="machine-slots"></div></div><div><span class="message-label">AI 视觉判断 · 示意</span><h3>红色指示灯亮起</h3><p>按配置规则，继续派发处理任务。</p></div></div>', note:'模型负责视觉判断 · 流程按配置推进'},
  {tag:'COORDINATE / 任务到人', title:'把问题，交给处理的人。', copy:'002 收到更换传感器的待办。人员完成现场处理后，通过语音确认任务完成，流程随即进入复查。', visual:'<div class="flow-task"><span class="message-label">任务 → 002</span><h3>请更换设备 001 的传感器</h3><p>人员完成处理后说：“完成第一个任务。”</p><div class="flow-mini-route"><span>收到待办</span><i>→</i><span>人工处理</span><i>→</i><span>确认完成</span></div></div>', note:'人员实际处理 · 语音提交完成状态'},
  {tag:'RECOVERY / 恢复确认', title:'处理之后，再确认一次。', copy:'服务端按配置次数重新采集温度。确认恢复后，向目标房间发送恢复文字和可选语音通知。', visual:'<div class="sensor-reading recovered"><span>001 / 复查温度</span><strong>28.4<small>°C</small></strong><div>✓ 已低于演示阈值 35°C</div><div class="recovery-message">房间广播：设备温度已恢复正常。</div></div>', note:'有限次数复查 · 恢复结果同步到房间'}
];
let workflowStep = 0;
function renderWorkflow() {
  const state = workflowStates[workflowStep];
  $('#workflow-content').innerHTML = `<div class="workflow-detail-tag">${state.tag}<span>0${workflowStep + 1}</span></div><h3>${state.title}</h3><p class="workflow-copy">${state.copy}</p>${state.visual}<div class="workflow-detail-note">${state.note}</div>`;
  document.querySelectorAll('.workflow-step').forEach((button,index) => {
    button.classList.toggle('active', index === workflowStep);
    button.classList.toggle('passed', index < workflowStep);
    button.setAttribute('aria-pressed', index === workflowStep);
  });
  $('#workflow-counter').textContent = `0${workflowStep + 1} / 05`;
  $('#workflow-next').innerHTML = workflowStep === 4 ? '重新探索 <span>↺</span>' : '查看下一步 <span>→</span>';
}
document.querySelectorAll('[data-step]').forEach(button => button.addEventListener('click', () => { workflowStep = Number(button.dataset.step); renderWorkflow(); }));
$('#workflow-next').addEventListener('click', () => { workflowStep = (workflowStep + 1) % workflowStates.length; renderWorkflow(); });
renderWorkflow();

demo.addEventListener('click', event => {
  if (!event.target.closest('.image-preview')) return;
  photoDialog.querySelector('img').src = 'assets/device-image.webp';
  photoDialog.querySelector('img').alt = photos.image[2];
  $('#photo-caption').textContent = photos.image[0];
  photoDialog.showModal();
});

demo.addEventListener('click', event => {
  const proof = event.target.closest('[data-proof]');
  if (!proof) return;
  const key = proof.dataset.proof;
  photoDialog.querySelector('img').src = `assets/device-${key}.webp`;
  photoDialog.querySelector('img').alt = photos[key][2];
  $('#photo-caption').textContent = photos[key][0];
  photoDialog.showModal();
});
