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
  $('#demo-talk')?.addEventListener('click', () => { talking = !talking; renderDemo(); });
  $('#hosting-toggle')?.addEventListener('click', () => { hosted = !hosted; renderDemo(); $('#hosting-toggle').focus({ preventScroll: true }); });
  document.querySelectorAll('[data-room]').forEach(button => button.addEventListener('click', () => { room = button.dataset.room; renderDemo(); }));
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
  transcript: ['声音之外，还有文字。', '对方的语音识别成文字，连同发言者、时长和回放入口呈现，方便回看近期沟通内容。', '实机显示设备 004 的 7 秒语音和识别文字'],
  hosting: ['模式变化，清楚可见。', '开启在线托管后，主按钮变为黄色。AI 按需协助回应，也可通过语音或点击请求退出。', '实机黄色托管模式按钮'],
  rooms: ['说出需求，选择协作空间。', '通过语音唤起房间列表，再触摸选择目标房间。让房间管理更直观，减少逐层寻找菜单。', '实机语音触发的房间选择列表']
};
let currentPhoto = 'idle';
document.querySelectorAll('[data-photo]').forEach(button => button.addEventListener('click', () => {
  currentPhoto = button.dataset.photo;
  const [title, description, alt] = photos[currentPhoto];
  $('#real-image').src = `assets/device-${currentPhoto}.png`;
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
