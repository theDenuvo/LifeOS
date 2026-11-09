// Concept C: Calm Life OS — Data & Interactions

const TASKS = [
  {id:1, title:"Подготовить презентацию для совета", note:"Слайды 1-15 готовы, нужны 16-20 про риски", priority:"high", deadline:"2026-11-10", project:"Работа", tags:["презентация","совет"], status:"active", created:"2026-11-08", time:"2ч"},
  {id:2, title:"Ответить на письма от клиентов", note:"3 непрочитанных от Acme Corp", priority:"high", deadline:"2026-11-09", project:"Работа", tags:["email","клиенты"], status:"active", created:"2026-11-09", time:"45м"},
  {id:3, title:"Купить продукты на ужин", note:"Сыр, томаты, базилик, спагетти", priority:"medium", deadline:"2026-11-09", project:"Личное", tags:["покупки"], status:"active", created:"2026-11-09", time:"30м"},
  {id:4, title:"Завершить курс AWS (40 часов)", note:"Дедлайн конец квартала", priority:"low", deadline:"2026-12-31", project:"Обучение", tags:["aws","карьера"], status:"active", created:"2026-11-01", time:"40ч"},
  {id:5, title:"Встреча с командой дизайна", note:"Обсудить новую дизайн-систему", priority:"high", deadline:"2026-11-09T14:00", project:"Работа", tags:["встреча","дизайн"], status:"active", created:"2026-11-08", time:"1ч"},
  {id:6, title:"Оплатить счета за интернет и связь", note:"Дедлайн был вчера", priority:"high", deadline:"2026-11-08", project:"Финансы", tags:["счета","просрочено"], status:"overdue", created:"2026-11-01", time:"15м"},
  {id:7, title:"Прочитать статью про RAG архитектуру", note:"Сохранено в Pocket", priority:"low", deadline:null, project:"Обучение", tags:["чтение","rag"], status:"active", created:"2026-11-05", time:"20м"},
  {id:8, title:"Записаться к стоматологу", note:"Профилактика, раз в полгода", priority:"medium", deadline:"2026-11-15", project:"Здоровье", tags:["врач"], status:"active", created:"2026-11-09", time:"10м"},
  {id:9, title:"Написать годовой отчёт", note:"Собрать метрики за Q3-Q4", priority:"high", deadline:"2026-11-20", project:"Работа", tags:["отчёт","квартал"], status:"active", created:"2026-11-09", time:"6ч"},
  {id:10, title:"Изучить Impeccable Skill", note:"Для дизайн-спринта LifeOS", priority:"high", deadline:"2026-11-09", project:"LifeOS", tags:["impeccable","дизайн"], status:"completed", created:"2026-11-09", completedAt:"2026-11-09T10:30", time:"2ч"}
];

const INBOX = [
  {text:"Позвонить маме про воскресенье", time:"09:12"},
  {text:"Идея: добавить в LifeOS режим фокуса с таймером помодоро", time:"09:45"},
  {text:"Книга \"Атомные привычки\" — перечитать главу 3", time:"10:30"},
  {text:"Проверить обновления зависимостей в проекте", time:"11:15"},
  {text:"Забронировать отель для конференции в декабре", time:"12:00"}
];

const ASSISTANT_MESSAGES = [
  {role:"assistant", content:"Доброе утро! На сегодня 5 задач, одна просрочена. Начнём с презентации?"},
  {role:"user", content:"Да, перенеси стоматолога на среду следующей недели утром"},
  {role:"assistant", content:"Готово, записал на 12 ноября 09:00. Ещё заметил: AWS сертификат без движения с 1 ноября. Разбить на этапы по 2 часа?"}
];

function formatDate(dateStr) {
  if (!dateStr) return "";
  const d = new Date(dateStr);
  const today = new Date("2026-11-09");
  const diff = Math.floor((d - today) / (1000*60*60*24));
  if (diff === 0) return "Сегодня";
  if (diff === 1) return "Завтра";
  if (diff === -1) return "Вчера";
  if (diff < 0) return `${Math.abs(diff)} дн. назад`;
  if (diff <= 7) return d.toLocaleDateString("ru-RU", {weekday:"short", day:"numeric", month:"short"});
  return d.toLocaleDateString("ru-RU", {day:"numeric", month:"short"});
}

function getPriorityLabel(p) {
  return p === "high" ? "Высокий" : p === "medium" ? "Средний" : "Низкий";
}

function showToast(message, type = "info") {
  const container = document.getElementById("toast-container");
  if (!container) return;
  const toast = document.createElement("div");
  toast.className = `toast toast--${type}`;
  toast.textContent = message;
  container.appendChild(toast);
  setTimeout(() => {
    toast.style.opacity = "0";
    setTimeout(() => toast.remove(), 300);
  }, 3000);
}

function switchScreen(screenName) {
  document.querySelectorAll(".screen").forEach(s => s.classList.remove("is-active"));
  const target = document.getElementById(`panel-${screenName}`);
  if (target) { target.classList.add("is-active"); target.removeAttribute("hidden"); }
  
  document.querySelectorAll(".bottom-nav__item").forEach(n => {
    n.classList.toggle("is-active", n.dataset.screen === screenName);
  });
  
  document.body.dataset.screen = screenName;
}

function renderToday() {
  const list = document.getElementById("today-list");
  if (!list) return;
  
  const activeTasks = TASKS.filter(t => t.status !== "completed");
  list.innerHTML = activeTasks.map(task => `
    <li class="calm-item ${task.status === 'overdue' ? 'is-overdue' : ''} ${task.priority === 'high' ? 'is-high-priority' : ''}" data-task-id="${task.id}" role="listitem">
      <div class="calm-item__checkbox ${task.status === 'completed' ? 'is-checked' : ''}" data-task-id="${task.id}" role="checkbox" aria-checked="${task.status === 'completed'}" tabindex="0"></div>
      <div class="calm-item__content">
        <h3 class="calm-item__title">${task.title}</h3>
        ${task.note ? `<p class="calm-item__note">${task.note}</p>` : ''}
        <div class="calm-item__meta">
          <span class="calm-item__project">${task.project}</span>
          ${task.deadline ? `<span class="calm-item__deadline ${task.status === 'overdue' ? 'is-overdue' : (task.deadline === '2026-11-09' || task.deadline === '2026-11-09T14:00' ? 'is-today' : '')}">${formatDate(task.deadline)}</span>` : ''}
          ${task.tags.map(t => `<span class="calm-item__tag">#${t}</span>`).join('')}
        </div>
      </div>
    </li>
  `).join('');
  
  const empty = document.getElementById("today-empty");
  if (empty) {
    empty.hidden = activeTasks.length > 0;
    list.hidden = activeTasks.length === 0;
  }
}

function renderInbox() {
  const list = document.getElementById("inbox-list");
  if (!list) return;
  list.innerHTML = INBOX.map((item, i) => `
    <li class="calm-item" data-inbox-index="${i}" role="listitem">
      <div class="calm-item__checkbox" data-inbox-index="${i}" role="checkbox" aria-checked="false" tabindex="0"></div>
      <div class="calm-item__content">
        <p class="calm-item__title">${item.text}</p>
        <div class="calm-item__meta">
          <span>${item.time}</span>
        </div>
      </div>
    </li>
  `).join('');
  
  const empty = document.getElementById("inbox-empty");
  if (empty) {
    empty.hidden = INBOX.length > 0;
    list.hidden = INBOX.length === 0;
  }
}

function renderAllTasks() {
  const list = document.getElementById("all-list");
  if (!list) return;
  
  const filter = document.querySelector(".calm-filter.is-active")?.dataset.filter || "all";
  let filtered = TASKS;
  if (filter === "active") filtered = TASKS.filter(t => t.status === "active");
  else if (filter === "overdue") filtered = TASKS.filter(t => t.status === "overdue");
  else if (filter === "completed") filtered = TASKS.filter(t => t.status === "completed");
  
  list.innerHTML = filtered.map(task => `
    <li class="calm-item ${task.status === 'overdue' ? 'is-overdue' : ''} ${task.priority === 'high' ? 'is-high-priority' : ''} ${task.status === 'completed' ? 'is-completed' : ''}" data-task-id="${task.id}" role="listitem">
      <div class="calm-item__checkbox ${task.status === 'completed' ? 'is-checked' : ''}" data-task-id="${task.id}" role="checkbox" aria-checked="${task.status === 'completed'}" tabindex="0"></div>
      <div class="calm-item__content">
        <h3 class="calm-item__title">${task.title}</h3>
        ${task.note ? `<p class="calm-item__note">${task.note}</p>` : ''}
        <div class="calm-item__meta">
          <span class="calm-item__project">${task.project}</span>
          ${task.deadline ? `<span class="calm-item__deadline ${task.status === 'overdue' ? 'is-overdue' : (task.deadline === '2026-11-09' || task.deadline === '2026-11-09T14:00' ? 'is-today' : '')}">${formatDate(task.deadline)}</span>` : ''}
          ${task.tags.map(t => `<span class="calm-item__tag">#${t}</span>`).join('')}
        </div>
      </div>
    </li>
  `).join('');
  
  const empty = document.getElementById("all-empty");
  if (empty) {
    empty.hidden = filtered.length > 0;
    list.hidden = filtered.length === 0;
  }
}

function renderAssistant() {
  const container = document.getElementById("assistant-messages");
  if (!container) return;
  container.innerHTML = ASSISTANT_MESSAGES.map(msg => `
    <div class="assistant-message assistant-message--${msg.role}">
      <div class="assistant-message__avatar">${msg.role === 'assistant' ? '🤖' : '👤'}</div>
      <div class="assistant-message__bubble">
        ${msg.content.split('\n').map(p => `<p>${p}</p>`).join('')}
      </div>
    </div>
  `).join('');
  container.scrollTop = container.scrollHeight;
}

function initEventHandlers() {
  // Screen switching
  document.querySelectorAll(".bottom-nav__item").forEach(el => {
    el.addEventListener("click", () => {
      const screen = el.dataset.screen;
      if (screen) switchScreen(screen);
    });
  });
  
  // Task checkbox toggle
  document.addEventListener("click", (e) => {
    const cb = e.target.closest(".calm-item__checkbox");
    if (cb && cb.dataset.taskId) {
      const taskId = parseInt(cb.dataset.taskId);
      const task = TASKS.find(t => t.id === taskId);
      if (task) {
        task.status = task.status === "completed" ? "active" : "completed";
        renderToday();
        renderAllTasks();
        showToast(task.status === "completed" ? "Выполнено" : "Восстановлено", "success");
      }
    }
  });
  
  // Filters
  document.querySelectorAll(".calm-filter").forEach(btn => {
    btn.addEventListener("click", () => {
      document.querySelectorAll(".calm-filter").forEach(b => b.classList.remove("is-active"));
      btn.classList.add("is-active");
      renderAllTasks();
    });
  });
  
  // Assistant composer
  const assistantInput = document.getElementById("assistant-input");
  const assistantComposer = document.getElementById("assistant-composer");
  if (assistantInput) {
    assistantInput.addEventListener("input", function() {
      this.style.height = "auto";
      this.style.height = Math.min(this.scrollHeight, 120) + "px";
    });
  }
  if (assistantComposer) {
    assistantComposer.addEventListener("submit", (e) => {
      e.preventDefault();
      const input = document.getElementById("assistant-input");
      const text = input.value.trim();
      if (!text) return;
      ASSISTANT_MESSAGES.push({role:"user", content: text});
      setTimeout(() => {
        ASSISTANT_MESSAGES.push({role:"assistant", content: `Понял: «${text}». Добавил в задачи. Хотите изменить дедлайн?`});
        renderAssistant();
      }, 700);
      input.value = ""; input.style.height = "auto";
      renderAssistant();
    });
  }
  
  // Capture area
  const captureInput = document.querySelector(".capture-area__input");
  const captureBtn = document.querySelector(".capture-area__btn");
  if (captureInput) {
    captureInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && captureInput.value.trim()) {
        showToast("Добавлено во входящие", "success");
        captureInput.value = "";
      }
    });
  }
  if (captureBtn) {
    captureBtn.addEventListener("click", () => {
      if (captureInput && captureInput.value.trim()) {
        showToast("Добавлено во входящие", "success");
        captureInput.value = "";
      }
    });
  }
  
  // Quick add
  document.querySelectorAll('[data-action="quick-add"]').forEach(btn => {
    btn.addEventListener("click", () => {
      const modal = document.getElementById("quick-add-modal");
      if (modal) modal.classList.add("is-open");
    });
  });
  
  const modal = document.getElementById("quick-add-modal");
  if (modal) {
    modal.querySelector(".modal__backdrop")?.addEventListener("click", () => modal.classList.remove("is-open"));
    modal.querySelector(".modal__close")?.addEventListener("click", () => modal.classList.remove("is-open"));
    modal.querySelector(".modal__cancel")?.addEventListener("click", () => modal.classList.remove("is-open"));
  }
  
  const quickAddForm = document.getElementById("quick-add-form");
  if (quickAddForm) {
    quickAddForm.addEventListener("submit", (e) => {
      e.preventDefault();
      const title = document.getElementById("qa-title").value.trim();
      if (!title) return;
      const newTask = {
        id: TASKS.length + 1, title: title,
        note: "", priority: document.getElementById("qa-priority").value,
        deadline: document.getElementById("qa-deadline").value || null,
        project: document.getElementById("qa-project").value,
        tags: [], status: "active", created: new Date().toISOString().split("T")[0], time: "15м"
      };
      TASKS.push(newTask);
      renderToday(); renderAllTasks();
      modal.classList.remove("is-open");
      quickAddForm.reset();
      showToast("Задача создана", "success");
    });
  }
}

function init() {
  renderToday();
  renderInbox();
  renderAllTasks();
  renderAssistant();
  initEventHandlers();
}

document.addEventListener("DOMContentLoaded", init);
