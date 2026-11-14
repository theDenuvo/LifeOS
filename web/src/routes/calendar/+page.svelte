<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { page } from '$app/stores';
	import { goto } from '$app/navigation';
	import {
		completeTask,
		createTask,
		deleteEvent,
		getCalendar,
		getTask,
		restoreTask,
		updateEvent,
		type CalendarConflict,
		type CalendarEvent,
		type CalendarName,
		type Priority,
		type Task
	} from '$lib/api';
	import { errorMessage, formatTime, priorityLabel, todayISO } from '$lib/format';
	import { liveRevision } from '$lib/live';
	import { selectedCalendars } from '$lib/calendarFilter';
	import {
		CalendarDays,
		Check,
		ChevronDown,
		ChevronLeft,
		ChevronRight,
		Pencil,
		Plus,
		Sparkles,
		Trash2,
		X
	} from 'lucide-svelte';

	const weekdays = ['Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб', 'Вс'];
	const dayMonthFmt = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long' });
	const weekdayLongFmt = new Intl.DateTimeFormat('ru-RU', { weekday: 'long' });
	const monthFmt = new Intl.DateTimeFormat('ru-RU', { month: 'long' });

	const initialParam = $page.url.searchParams.get('date');
	const hasInitialDate = Boolean(initialParam && /^\d{4}-\d{2}-\d{2}$/.test(initialParam ?? ''));
	const initialDate = hasInitialDate ? parseDay(initialParam!) : new Date();

	type GridEntry = {
		key: string;
		kind: 'event' | 'task';
		title: string;
		time: string;
		tone?: string;
		calendar?: CalendarName;
		conflict: boolean;
	};

	let tasks: Task[] = [];
	let events: CalendarEvent[] = [];
	let conflicts: CalendarConflict[] = [];
	let loading = true;
	let error = '';
	let cursor = new Date(initialDate.getFullYear(), initialDate.getMonth(), 1);
	let selected = hasInitialDate ? initialParam! : todayISO();
	let busyId: number | null = null;
	let showCreate = false;
	let newTitle = '';
	let newPriority: Priority = 'medium';
	let creating = false;
	let createInput: HTMLInputElement;
	// Event editing: the editor is inline in the day panel, so the event stays in
	// sight while it is changed. Deletion is a hard delete (no archive, no
	// "hidden" flag), which is why it needs a second, explicit confirmation.
	let editingId = 0;
	let editTitle = '';
	let editDate = '';
	let editStart = '';
	let editEnd = '';
	let editAllDay = false;
	let editCalendar: CalendarName = 'personal';
	let editLocation = '';
	let editDescription = '';
	let editSaving = false;
	let editErrorText = '';
	let confirmingDelete = 0;
	let deletingId = 0;
	let eventNotice = '';
	let loadedMonth = '';
	let loadToken = 0;

	const monthOptions = Array.from({ length: 25 }, (_, index) => {
		const now = new Date();
		const date = new Date(now.getFullYear(), now.getMonth() - 12 + index, 1);
		return {
			value: monthKey(date),
			label: monthTitle(date)
		};
	});

	$: monthStart = new Date(cursor.getFullYear(), cursor.getMonth(), 1);
	$: monthLabel = monthTitle(monthStart);
	$: monthKeyValue = monthKey(monthStart);
	$: visibleEvents = events.filter((event) => $selectedCalendars.includes(event.calendar));
	$: monthEvents = visibleEvents.filter((event) => eventDate(event).slice(0, 7) === monthKeyValue);
	$: monthTasks = tasks.filter((task) => {
		const key = taskDate(task);
		return key ? key.slice(0, 7) === monthKeyValue : false;
	});
	$: openMonth = monthTasks.filter((task) => task.status === 'open').length;
	$: conflictEventIDs = new Set(conflicts.flatMap((conflict) => conflict.event_ids));
	$: grid = buildGrid(monthStart, selected, tasks, visibleEvents, conflictEventIDs);
	$: dayTasks = tasks
		.filter((task) => taskDate(task) === selected)
		.sort((a, b) => (a.due_at ?? '').localeCompare(b.due_at ?? ''));
	$: openDay = dayTasks.filter((task) => task.status === 'open');
	$: doneDay = dayTasks.filter((task) => task.status !== 'open');
	$: dayEvents = visibleEvents
		.filter((event) => eventDate(event) === selected)
		.sort((a, b) => a.starts_at.localeCompare(b.starts_at));
	$: dayConflicts = conflicts.filter((conflict) =>
		conflict.event_ids.some((id) => dayEvents.some((event) => event.id === id))
	);
	$: progress = dayTasks.length > 0 ? Math.round((doneDay.length / dayTasks.length) * 100) : 0;
	$: editingEvent = editingId ? (events.find((item) => item.id === editingId) ?? null) : null;
	$: selectedLabel = capitalize(
		`${weekdayLongFmt.format(parseDay(selected))}, ${dayMonthFmt.format(parseDay(selected))}`
	);

	$: if (monthKeyValue !== loadedMonth) {
		loadedMonth = monthKeyValue;
		void load(monthStart);
	}

	function capitalize(value: string): string {
		return value.charAt(0).toUpperCase() + value.slice(1);
	}

	function monthTitle(date: Date): string {
		return `${capitalize(monthFmt.format(date))} ${date.getFullYear()}`;
	}

	function plural(count: number, one: string, few: string, many: string): string {
		const mod10 = count % 10;
		const mod100 = count % 100;
		if (mod10 === 1 && mod100 !== 11) return one;
		if (mod10 >= 2 && mod10 <= 4 && (mod100 < 10 || mod100 >= 20)) return few;
		return many;
	}

	function taskWord(count: number): string {
		return plural(count, 'задача', 'задачи', 'задач');
	}

	function eventWord(count: number): string {
		return plural(count, 'событие', 'события', 'событий');
	}

	function weekWord(count: number): string {
		return plural(count, 'неделя', 'недели', 'недель');
	}

	function isoKey(date: Date): string {
		const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
		return local.toISOString().slice(0, 10);
	}

	function monthKey(date: Date): string {
		return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}`;
	}

	function parseDay(key: string): Date {
		return new Date(`${key}T00:00:00`);
	}

	function taskDate(task: Task): string | null {
		if (task.due_date) return task.due_date;
		if (task.planned_date) return task.planned_date;
		if (task.due_at) return isoKey(new Date(task.due_at));
		return null;
	}

	function eventDate(event: CalendarEvent): string {
		return isoKey(new Date(event.starts_at));
	}

	function eventTime(event: CalendarEvent): string {
		if (event.all_day) return 'Весь день';
		return `${formatTime(event.starts_at)}–${formatTime(event.ends_at)}`;
	}

	function eventCalendarLabel(event: CalendarEvent): string {
		if (event.calendar === 'work') return 'Работа';
		if (event.calendar === 'meetings') return 'Встречи';
		return 'Личное';
	}

	function getGridBounds(start: Date) {
		const offset = (start.getDay() + 6) % 7;
		const daysInMonth = new Date(start.getFullYear(), start.getMonth() + 1, 0).getDate();
		const weeks = Math.ceil((offset + daysInMonth) / 7);
		const first = new Date(start);
		first.setDate(first.getDate() - offset);
		const last = new Date(first);
		last.setDate(first.getDate() + weeks * 7 - 1);
		return { from: isoKey(first), to: isoKey(last), weeks };
	}

	function buildGrid(
		start: Date,
		selectedKey: string,
		taskList: Task[],
		eventList: CalendarEvent[],
		conflictIDs: Set<number>
	) {
		const offset = (start.getDay() + 6) % 7;
		const daysInMonth = new Date(start.getFullYear(), start.getMonth() + 1, 0).getDate();
		const weeks = Math.ceil((offset + daysInMonth) / 7);
		const first = new Date(start);
		first.setDate(first.getDate() - offset);

		const days = Array.from({ length: weeks * 7 }, (_, index) => {
			const date = new Date(first);
			date.setDate(first.getDate() + index);
			const key = isoKey(date);
			const dayTasks = taskList.filter((task) => taskDate(task) === key);
			const dayEvents = eventList.filter((event) => eventDate(event) === key);
			const entries: GridEntry[] = [
				...dayEvents.map((event) => ({
					key: `e-${event.id}`,
					kind: 'event' as const,
					title: event.title,
					time: event.starts_at,
					calendar: event.calendar,
					conflict: conflictIDs.has(event.id)
				})),
				...dayTasks.map((task) => ({
					key: `t-${task.id}`,
					kind: 'task' as const,
					title: task.title,
					time: task.due_at ?? '',
					tone: tone(task),
					conflict: false
				}))
			].sort((a, b) => (a.time || '').localeCompare(b.time || ''));
			return {
				key,
				date,
				day: date.getDate(),
				inMonth: date.getMonth() === start.getMonth(),
				today: key === todayISO(),
				selected: key === selectedKey,
				tasks: dayTasks,
				events: dayEvents,
				entries
			};
		});

		return { days, weeks };
	}

	function tone(task: Task): string {
		if (task.status === 'completed') return 'done';
		if (task.overdue || (taskDate(task) ?? '') < todayISO()) return 'overdue';
		if (task.priority === 'high') return 'accent';
		return 'info';
	}

	async function load(start = monthStart) {
		const token = ++loadToken;
		loading = true;
		tasks = [];
		events = [];
		conflicts = [];
		try {
			const bounds = getGridBounds(start);
			const result = await getCalendar(bounds.from, bounds.to);
			if (token !== loadToken) return;
			tasks = result.tasks;
			events = result.events;
			conflicts = result.conflicts;
			error = '';
		} catch (err) {
			if (token !== loadToken) return;
			error = errorMessage(err);
		} finally {
			if (token === loadToken) loading = false;
		}
	}

	function selectDay(key: string) {
		selected = key;
		const date = parseDay(key);
		if (date.getMonth() !== cursor.getMonth() || date.getFullYear() !== cursor.getFullYear()) {
			cursor = new Date(date.getFullYear(), date.getMonth(), 1);
		}
		goto(`/calendar?date=${key}`, { replaceState: true, noScroll: true });
	}

	function shiftMonth(delta: number) {
		const next = new Date(cursor.getFullYear(), cursor.getMonth() + delta, 1);
		cursor = next;
		selected = isoKey(next);
		syncURL();
	}

	function goToday() {
		const today = new Date();
		cursor = new Date(today.getFullYear(), today.getMonth(), 1);
		selected = todayISO();
		syncURL();
	}

	function changeMonth(value: string) {
		const parts = value.split('-');
		const year = Number(parts[0]);
		const month = Number(parts[1]);
		cursor = new Date(year, month - 1, 1);
		selected = monthKey(new Date()) === value ? todayISO() : isoKey(cursor);
		syncURL();
	}

	function syncURL() {
		goto(`/calendar?date=${selected}`, { replaceState: true, noScroll: true });
	}

	async function toggle(task: Task) {
		if (busyId !== null) return;
		busyId = task.id;
		error = '';
		try {
			const fresh = await getTask(task.id);
			if (fresh.status === 'open') {
				await completeTask(fresh.id, fresh.version);
			} else {
				await restoreTask(fresh.id, fresh.version);
			}
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function create() {
		const title = newTitle.trim();
		if (!title || creating) return;
		creating = true;
		error = '';
		try {
			await createTask({ title, priority: newPriority, due_date: selected });
			newTitle = '';
			newPriority = 'medium';
			showCreate = false;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			creating = false;
		}
	}

	async function openCreate() {
		showCreate = true;
		await tick();
		createInput?.focus();
	}

	function two(value: number): string {
		return String(value).padStart(2, '0');
	}

	function localDateKey(date: Date): string {
		return `${date.getFullYear()}-${two(date.getMonth() + 1)}-${two(date.getDate())}`;
	}

	function localTimeKey(date: Date): string {
		return `${two(date.getHours())}:${two(date.getMinutes())}`;
	}

	function combineDateTime(day: string, time: string): Date | null {
		if (!day || !time) return null;
		const value = new Date(`${day}T${time}:00`);
		return Number.isNaN(value.getTime()) ? null : value;
	}

	function startEdit(event: CalendarEvent) {
		const starts = new Date(event.starts_at);
		const ends = new Date(event.ends_at);
		editingId = event.id;
		editTitle = event.title;
		editDate = localDateKey(starts);
		editStart = localTimeKey(starts);
		editEnd = localTimeKey(ends);
		editAllDay = event.all_day;
		editCalendar = event.calendar;
		editLocation = event.location ?? '';
		editDescription = event.description ?? '';
		editErrorText = '';
		confirmingDelete = 0;
		eventNotice = '';
		showCreate = false;
	}

	function closeEditor() {
		editingId = 0;
		confirmingDelete = 0;
		editErrorText = '';
	}

	async function saveEvent() {
		if (!editingId || editSaving) return;
		const title = editTitle.trim();
		if (!title) {
			editErrorText = 'Название не может быть пустым';
			return;
		}
		const starts = editAllDay ? new Date(`${editDate}T00:00:00`) : combineDateTime(editDate, editStart);
		if (!starts) {
			editErrorText = 'Укажите дату и время начала';
			return;
		}
		const dayLength = 24 * 60 * 60 * 1000;
		let ends = editAllDay ? new Date(starts.getTime() + dayLength) : combineDateTime(editDate, editEnd);
		if (!ends || ends.getTime() <= starts.getTime()) {
			ends = new Date(starts.getTime() + (editAllDay ? dayLength : 60 * 60 * 1000));
		}
		editSaving = true;
		editErrorText = '';
		eventNotice = '';
		try {
			const updated = await updateEvent(editingId, editVersionOf(editingId), {
				title,
				starts_at: starts.toISOString(),
				ends_at: ends.toISOString(),
				all_day: editAllDay,
				calendar: editCalendar,
				location: editLocation.trim(),
				description: editDescription.trim()
			});
			// Follow the event to its new day instead of leaving the user on an
			// empty panel, which used to look like the edit had failed.
			const moved = localDateKey(new Date(updated.starts_at));
			closeEditor();
			if (moved !== selected) selectDay(moved);
			await load(parseDay(moved));
			eventNotice = 'Событие сохранено';
		} catch (err) {
			editErrorText = errorMessage(err);
			await load();
		} finally {
			editSaving = false;
		}
	}

	function editVersionOf(id: number): number {
		return events.find((event) => event.id === id)?.version ?? 1;
	}

	function askDelete(id: number) {
		confirmingDelete = id;
		eventNotice = '';
	}

	async function removeEvent(event: CalendarEvent) {
		if (deletingId) return;
		deletingId = event.id;
		editErrorText = '';
		try {
			await deleteEvent(event.id, event.version);
			if (editingId === event.id) closeEditor();
			confirmingDelete = 0;
			await load();
			eventNotice = `Событие «${event.title}» удалено безвозвратно`;
		} catch (err) {
			editErrorText = errorMessage(err);
			confirmingDelete = 0;
			await load();
		} finally {
			deletingId = 0;
		}
	}

	onMount(() => {
		let timer: ReturnType<typeof setTimeout> | undefined;
		const unsubscribe = liveRevision.subscribe((revision) => {
			if (revision === 0) return;
			clearTimeout(timer);
			timer = setTimeout(() => void load(), 250);
		});

		return () => {
			unsubscribe();
			clearTimeout(timer);
		};
	});
</script>

<svelte:head>
	<title>Календарь — LifeOS</title>
</svelte:head>

<div class="calendar-page">
	<header class="page-head">
		<div class="head-copy">
			<h1>Календарь и задачи</h1>
			<p>{monthLabel} · {monthEvents.length} {eventWord(monthEvents.length)} · {monthTasks.length} {taskWord(monthTasks.length)} · {openMonth} открыто</p>
		</div>
		<div class="head-actions">
			<label class="month-select">
				<CalendarDays size={13} strokeWidth={1.7} aria-hidden="true" />
				<select
					value={monthKeyValue}
					aria-label="Выбрать месяц"
					on:change={(event) => changeMonth(event.currentTarget.value)}
				>
					{#each monthOptions as option (option.value)}
						<option value={option.value}>{option.label}</option>
					{/each}
				</select>
				<ChevronDown size={12} strokeWidth={1.8} aria-hidden="true" />
			</label>

			<div class="segmented">
				<button type="button" aria-label="Предыдущий месяц" on:click={() => shiftMonth(-1)}>
					<ChevronLeft size={14} strokeWidth={1.8} />
				</button>
				<button type="button" class="today-button" on:click={goToday}>Сегодня</button>
				<button type="button" aria-label="Следующий месяц" on:click={() => shiftMonth(1)}>
					<ChevronRight size={14} strokeWidth={1.8} />
				</button>
			</div>

			<button type="button" class="primary" on:click={openCreate}>
				<Plus size={14} strokeWidth={2} />
				Создать задачу
			</button>
		</div>
	</header>

	{#if error}
		<div class="error-banner" role="alert">
			<span>{error}</span>
			<div class="error-actions">
				<button type="button" class="retry" on:click={() => { error = ''; load(); }}>Повторить</button>
				<button type="button" aria-label="Закрыть" on:click={() => (error = '')}>×</button>
			</div>
		</div>
	{/if}

	{#if showCreate}
		<form class="create-bar" on:submit|preventDefault={create}>
			<input
				bind:this={createInput}
				bind:value={newTitle}
				placeholder={`Новая задача на ${dayMonthFmt.format(parseDay(selected))}`}
				aria-label="Название задачи"
				autocomplete="off"
			/>
			<select bind:value={newPriority} aria-label="Приоритет">
				<option value="high">Высокий</option>
				<option value="medium">Средний</option>
				<option value="low">Низкий</option>
			</select>
			<button type="submit" class="primary" disabled={creating || !newTitle.trim()}>
				{creating ? 'Создаю…' : 'Создать'}
			</button>
			<button type="button" class="ghost" on:click={() => (showCreate = false)}>Отмена</button>
		</form>
	{/if}

	<div class="calendar-grid">
		<section class="calendar-card" aria-label="Месяц">
			<div class="calendar-head">
				<div class="calendar-title">
					<h2>{monthLabel}</h2>
					<p>{grid.weeks} {weekWord(grid.weeks)} · {monthEvents.length} {eventWord(monthEvents.length)} · {monthTasks.length} {taskWord(monthTasks.length)}</p>
				</div>
				<div class="legend" aria-label="Обозначения">
					<span><i class="dot dot--accent"></i>Встречи</span>
					<span><i class="dot dot--info"></i>Задачи</span>
					<span><i class="dot dot--warning"></i>Совпадения</span>
				</div>
				<div class="calendar-arrows">
					<button type="button" aria-label="Предыдущий месяц" on:click={() => shiftMonth(-1)}>
						<ChevronLeft size={13} strokeWidth={1.8} />
					</button>
					<button type="button" aria-label="Следующий месяц" on:click={() => shiftMonth(1)}>
						<ChevronRight size={13} strokeWidth={1.8} />
					</button>
				</div>
			</div>

			<div class="weekday-row" aria-hidden="true">
				{#each weekdays as weekday}
					<span>{weekday}</span>
				{/each}
			</div>

			<div class="month-grid" aria-label={monthLabel}>
				{#each grid.days as day (day.key)}
					<button
						type="button"
						class="day-cell"
						class:outside={!day.inMonth}
						class:selected={day.selected}
						class:today={day.today}
						aria-pressed={day.selected}
						aria-label={`${day.day}, задач: ${day.tasks.length}, событий: ${day.events.length}`}
						on:click={() => selectDay(day.key)}
					>
						<span class="day-num">{day.day}</span>
						{#if day.entries.length > 0}
							<span class="day-events">
								{#each day.entries.slice(0, 2) as entry (entry.key)}
									<span
										class="event"
										data-kind={entry.kind}
										data-tone={entry.tone}
										data-calendar={entry.calendar}
										data-conflict={entry.conflict}
										title={entry.title}
									>{entry.title}</span>
								{/each}
								{#if day.entries.length > 2}
									<span class="more">+{day.entries.length - 2}</span>
								{/if}
							</span>
						{/if}
					</button>
				{/each}
			</div>
		</section>

		<section class="day-card" aria-label="Задачи дня">
			<div class="day-head">
				<div class="day-title">
					<h2>Задачи дня</h2>
					<p>{selectedLabel} · {dayTasks.length} {taskWord(dayTasks.length)} · {dayEvents.length} {eventWord(dayEvents.length)}</p>
				</div>
				<div class="pills">
					<span class="pill pill--accent">
						{openDay.length} {plural(openDay.length, 'открытая', 'открытых', 'открытых')}
					</span>
					<span class="pill">{doneDay.length} готово</span>
				</div>
			</div>

			<div class="day-progress">
				<div class="progress-label">
					<span>Прогресс дня</span>
					<strong>{progress}%</strong>
				</div>
				<div class="progress"><span style={`width:${progress}%`}></span></div>
			</div>

			{#if editingId}
				<form class="event-editor" on:submit|preventDefault={saveEvent} aria-label="Редактирование события">
					<div class="editor-head">
						<h3>Событие</h3>
						<button
							type="button"
							class="event-action"
							aria-label="Закрыть редактор"
							title="Закрыть"
							on:click={closeEditor}
						>
							<X size={12} strokeWidth={2} />
						</button>
					</div>

					<label class="field field--wide">
						<span>Название</span>
						<input bind:value={editTitle} maxlength="200" autocomplete="off" />
					</label>

					<div class="field-row">
						<label class="field">
							<span>Дата</span>
							<input type="date" bind:value={editDate} />
						</label>
						<label class="field">
							<span>Начало</span>
							<input type="time" bind:value={editStart} disabled={editAllDay} />
						</label>
						<label class="field">
							<span>Конец</span>
							<input type="time" bind:value={editEnd} disabled={editAllDay} />
						</label>
					</div>

					<div class="field-row">
						<label class="field">
							<span>Календарь</span>
							<select bind:value={editCalendar}>
								<option value="work">Работа</option>
								<option value="meetings">Встречи</option>
								<option value="personal">Личное</option>
							</select>
						</label>
						<label class="field field--check">
							<input type="checkbox" bind:checked={editAllDay} />
							<span>Весь день</span>
						</label>
					</div>

					<label class="field field--wide">
						<span>Место</span>
						<input bind:value={editLocation} maxlength="200" autocomplete="off" />
					</label>

					<label class="field field--wide">
						<span>Описание</span>
						<textarea bind:value={editDescription} rows="2" maxlength="2000"></textarea>
					</label>

					{#if editErrorText}
						<p class="edit-error" role="alert">{editErrorText}</p>
					{/if}

					<div class="editor-actions">
						<button type="submit" class="primary" disabled={editSaving}>
							{editSaving ? 'Сохраняю…' : 'Сохранить'}
						</button>
						<button type="button" class="ghost" on:click={closeEditor}>Отмена</button>
						<span class="editor-spacer"></span>
						{#if confirmingDelete === editingId}
							<button
								type="button"
								class="ghost danger-text"
								disabled={deletingId === editingId}
								on:click={() => editingEvent && removeEvent(editingEvent)}
							>{deletingId === editingId ? 'Удаляю…' : 'Точно удалить'}</button>
							<button type="button" class="ghost" on:click={() => (confirmingDelete = 0)}>Не удалять</button>
						{:else}
							<button type="button" class="ghost danger-text" on:click={() => askDelete(editingId)}>
								Удалить безвозвратно
							</button>
						{/if}
					</div>
					<p class="editor-hint">Удаление окончательное: событие исчезнет из календаря, сводок и ответов ассистента.</p>
				</form>
			{/if}

			{#if eventNotice}
				<p class="event-notice" role="status">
					<Check size={12} strokeWidth={2.2} />
					{eventNotice}
					<button type="button" class="notice-close" aria-label="Скрыть уведомление" on:click={() => (eventNotice = '')}>
						<X size={11} strokeWidth={2.2} />
					</button>
				</p>
			{/if}

			<div class="day-scroll">
				{#if loading && tasks.length === 0}
					<div class="day-skeleton">
						<span class="skeleton-line"></span>
						<span class="skeleton-line"></span>
						<span class="skeleton-line skeleton-line--medium"></span>
					</div>
				{:else if dayTasks.length === 0 && dayEvents.length === 0}
					<p class="day-empty">На этот день задач нет. Добавьте первую — она появится в календаре.</p>
				{:else}
					{#if dayEvents.length > 0}
						<h3 class="group-title">Встречи <span>{dayEvents.length}</span></h3>
						<ul class="event-list">
							{#each dayEvents as event (event.id)}
								<li class:conflict={conflictEventIDs.has(event.id)} class:editing={editingId === event.id}>
									<button
										type="button"
										class="event-open"
										on:click={() => startEdit(event)}
										aria-label={`Изменить событие «${event.title}»`}
									>
										<span class="event-time">{eventTime(event)}</span>
										<span class="task-copy">
											<span class="task-title">{event.title}</span>
											<span class="task-meta">
												{eventCalendarLabel(event)}
												{#if event.location} · {event.location}{/if}
											</span>
										</span>
									</button>
									{#if conflictEventIDs.has(event.id)}
										<span class="conflict-badge">Совпадение</span>
									{/if}
									<span class="event-actions">
										{#if confirmingDelete === event.id}
											<button
												type="button"
												class="event-action event-action--danger"
												disabled={deletingId === event.id}
												on:click={() => removeEvent(event)}
											>{deletingId === event.id ? 'Удаляю…' : 'Удалить навсегда'}</button>
											<button type="button" class="event-action" on:click={() => (confirmingDelete = 0)}>
												Отмена
											</button>
										{:else}
											<button
												type="button"
												class="event-action"
												title="Изменить событие"
												aria-label={`Изменить событие «${event.title}»`}
												on:click={() => startEdit(event)}
											>
												<Pencil size={12} strokeWidth={1.9} />
											</button>
											<button
												type="button"
												class="event-action event-action--danger"
												title="Удалить событие безвозвратно"
												aria-label={`Удалить событие «${event.title}»`}
												disabled={deletingId === event.id}
												on:click={() => askDelete(event.id)}
											>
												<Trash2 size={12} strokeWidth={1.9} />
											</button>
										{/if}
									</span>
								</li>
							{/each}
						</ul>
					{/if}

					{#if openDay.length > 0}
						<h3 class="group-title">Открытые <span>{openDay.length}</span></h3>
						<ul class="task-list">
							{#each openDay as task (task.id)}
								<li>
									<button
										type="button"
										class="check"
										aria-label={`Завершить задачу «${task.title}»`}
										disabled={busyId === task.id}
										on:click={() => toggle(task)}
									></button>
									<div class="task-copy">
										<span class="task-title">{task.title}</span>
										<span class="task-meta">
											<span class="priority" data-priority={task.priority}>
												<i></i>{priorityLabel(task.priority)}
											</span>
											{#if task.due_at}
												<span>· {formatTime(task.due_at)}</span>
											{:else}
												<span>· без времени</span>
											{/if}
										</span>
									</div>
								</li>
							{/each}
						</ul>
					{/if}

					{#if doneDay.length > 0}
						<h3 class="group-title">Выполнено <span>{doneDay.length}</span></h3>
						<ul class="task-list task-list--done">
							{#each doneDay as task (task.id)}
								<li>
									<button
										type="button"
										class="check checked"
										aria-label={`Вернуть задачу «${task.title}»`}
										disabled={busyId === task.id}
										on:click={() => toggle(task)}
									>
										<Check size={11} strokeWidth={2.4} />
									</button>
									<div class="task-copy">
										<span class="task-title">{task.title}</span>
										<span class="task-meta">
											{task.status === 'completed' ? 'Выполнено' : 'Отменено'}
											{#if task.due_at} · {formatTime(task.due_at)}{/if}
										</span>
									</div>
								</li>
							{/each}
						</ul>
					{/if}
				{/if}

				<button type="button" class="add-row" on:click={openCreate}>
					<Plus size={13} strokeWidth={2} />
					Добавить задачу на этот день
				</button>
			</div>
		</section>
	</div>

	<footer class="calendar-footer">
		<span class="footer-sync">
			<Sparkles size={12} strokeWidth={1.7} />
			Ассистент синхронизирует задачи с календарём в реальном времени
		</span>
		<span>Показано {dayTasks.length} из {tasks.length} задач · {dayEvents.length} событий · обновлено только что</span>
	</footer>
</div>

<style>
	.calendar-page {
		display: flex;
		height: 100dvh;
		min-height: 0;
		flex-direction: column;
		padding: 0 24px;
		background: var(--bg-primary);
	}

	.page-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 16px;
		height: 56px;
		min-height: 56px;
	}

	.head-copy {
		min-width: 0;
	}

	.head-copy h1 {
		margin: 0;
		font-size: var(--text-2xl);
		font-weight: 650;
		letter-spacing: -0.02em;
	}

	.head-copy p {
		margin: 1px 0 0;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.head-actions {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-shrink: 0;
	}

	.month-select {
		display: flex;
		align-items: center;
		gap: 5px;
		height: 28px;
		padding: 0 6px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--bg-panel);
		color: var(--fg-muted);
	}

	.month-select select {
		border: none;
		background: transparent;
		padding: 0 2px;
		color: var(--fg-body);
		font-size: var(--text-xs);
		outline: none;
		appearance: none;
		cursor: pointer;
	}

	.month-select select:focus {
		box-shadow: none;
	}

	.month-select :global(svg) {
		flex-shrink: 0;
	}

	.segmented {
		display: flex;
		align-items: center;
		height: 28px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--bg-panel);
		overflow: hidden;
	}

	.segmented button {
		display: grid;
		place-items: center;
		height: 100%;
		min-width: 26px;
		padding: 0 4px;
		border: none;
		background: transparent;
		color: var(--fg-muted);
	}

	.segmented button:hover {
		background: var(--bg-raised);
		color: var(--fg-primary);
	}

	.segmented .today-button {
		padding: 0 8px;
		border-left: 1px solid var(--border-soft);
		border-right: 1px solid var(--border-soft);
		color: var(--fg-body);
		font-size: 9px;
		letter-spacing: 0.09em;
		text-transform: uppercase;
		white-space: nowrap;
	}

	.primary {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		height: 30px;
		padding: 0 10px;
		border: 1px solid var(--accent);
		border-radius: 6px;
		background: var(--accent);
		color: #fff;
		font-size: var(--text-xs);
		font-weight: 550;
		white-space: nowrap;
	}

	.primary:hover:not(:disabled) {
		background: var(--accent-bright);
		border-color: var(--accent-bright);
	}

	.primary:disabled {
		opacity: 0.5;
	}

	.ghost {
		height: 30px;
		padding: 0 10px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: transparent;
		color: var(--fg-muted);
		font-size: var(--text-xs);
	}

	.ghost:hover {
		color: var(--fg-primary);
		border-color: var(--ink-700);
	}

	.error-banner {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 12px;
		margin-bottom: 8px;
		padding: 7px 10px;
		border: 1px solid var(--danger-border);
		border-radius: 7px;
		background: var(--danger-surface);
		color: var(--danger-text);
		font-size: var(--text-xs);
		flex-shrink: 0;
	}

	.error-actions {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.error-banner button {
		border: none;
		background: transparent;
		color: inherit;
		font-size: 15px;
	}

	.error-banner .retry {
		font-size: var(--text-xs);
		font-weight: 550;
		text-decoration: underline;
		text-underline-offset: 2px;
	}

	.create-bar {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-bottom: 8px;
		padding: 8px;
		border: 1px solid var(--border);
		border-radius: 8px;
		background: var(--bg-panel);
		flex-shrink: 0;
	}

	.create-bar input {
		flex: 1;
		min-width: 0;
		height: 30px;
		padding: 0 9px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--bg-raised);
		font-size: var(--text-sm);
	}

	.create-bar select {
		height: 30px;
		padding: 0 6px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--bg-raised);
		font-size: var(--text-xs);
	}

	.calendar-grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(360px, 1fr);
		gap: 14px;
		flex: 1;
		min-height: 0;
	}

	.calendar-card,
	.day-card {
		display: flex;
		min-width: 0;
		min-height: 0;
		flex-direction: column;
		padding: 14px;
		border: 1px solid var(--border);
		border-radius: 11px;
		background: var(--ink-850);
	}

	.calendar-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 10px;
		margin-bottom: 12px;
	}

	.calendar-title h2 {
		margin: 0;
		font-size: var(--text-base);
		font-weight: 600;
	}

	.calendar-title p {
		margin: 2px 0 0;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.legend {
		display: flex;
		align-items: center;
		gap: 9px;
		margin-top: 2px;
		color: var(--fg-faint);
		font-size: 9px;
		white-space: nowrap;
	}

	.legend span {
		display: inline-flex;
		align-items: center;
		gap: 4px;
	}

	.dot {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--fg-faint);
	}

	.dot--accent {
		background: var(--accent);
	}

	.dot--info {
		background: var(--info);
	}

	.dot--warning {
		background: var(--warning);
	}

	.calendar-arrows {
		display: flex;
		gap: 3px;
	}

	.calendar-arrows button {
		display: grid;
		place-items: center;
		width: 24px;
		height: 24px;
		border: 1px solid var(--border);
		border-radius: 5px;
		background: transparent;
		color: var(--fg-muted);
	}

	.calendar-arrows button:hover {
		background: var(--bg-raised);
		color: var(--fg-primary);
	}

	.weekday-row {
		display: grid;
		grid-template-columns: repeat(7, minmax(0, 1fr));
		padding-bottom: 6px;
		color: var(--fg-faint);
		font-size: 9px;
		text-align: left;
	}

	.weekday-row span {
		padding-left: 4px;
	}

	.month-grid {
		display: grid;
		flex: 1;
		min-height: 0;
		grid-template-columns: repeat(7, minmax(0, 1fr));
		grid-auto-rows: minmax(62px, 1fr);
		border-top: 1px solid var(--border-soft);
		border-left: 1px solid var(--border-soft);
	}

	.day-cell {
		display: flex;
		min-width: 0;
		flex-direction: column;
		align-items: stretch;
		gap: 4px;
		padding: 5px;
		border: none;
		border-right: 1px solid var(--border-soft);
		border-bottom: 1px solid var(--border-soft);
		background: transparent;
		color: var(--fg-body);
		text-align: left;
		overflow: hidden;
	}

	.day-cell:hover {
		background: var(--bg-raised);
	}

	.day-cell.outside {
		color: var(--fg-faint);
		opacity: 0.55;
	}

	.day-cell.today .day-num {
		color: var(--accent-bright);
		font-weight: 650;
	}

	.day-cell.selected {
		box-shadow: inset 0 0 0 1px var(--accent);
		background: rgba(124, 92, 252, 0.08);
	}

	.day-num {
		font-size: var(--text-xs);
		font-variant-numeric: tabular-nums;
		line-height: 1.1;
	}

	.day-events {
		display: flex;
		min-width: 0;
		flex-direction: column;
		gap: 2px;
		overflow: hidden;
	}

	.event {
		position: relative;
		display: block;
		padding: 2px 3px 2px 8px;
		border-radius: 3px;
		background: var(--accent-surface);
		color: var(--accent-bright);
		font-size: 8px;
		line-height: 1.25;
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}

	.event::before {
		content: '';
		position: absolute;
		left: 4px;
		top: 50%;
		width: 3px;
		height: 3px;
		margin-top: -1.5px;
		border-radius: 50%;
		background: currentColor;
	}

	.event[data-tone='overdue'] {
		background: var(--danger-surface);
		color: #fca5a5;
	}

	.event[data-tone='done'] {
		background: var(--success-surface);
		color: #86efac;
	}

	.event[data-tone='info'] {
		background: rgba(34, 211, 238, 0.1);
		color: #67e8f9;
	}

	.event[data-kind='event'][data-calendar='work'] {
		background: var(--accent-surface);
		color: var(--accent-bright);
	}

	.event[data-kind='event'][data-calendar='meetings'] {
		background: var(--success-surface);
		color: #86efac;
	}

	.event[data-kind='event'][data-calendar='personal'] {
		background: rgba(34, 211, 238, 0.1);
		color: #67e8f9;
	}

	.event[data-conflict='true'] {
		box-shadow: inset 0 0 0 1px var(--warning);
	}

	.more {
		padding-left: 4px;
		color: var(--fg-faint);
		font-size: 8px;
	}

	.day-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 10px;
	}

	.day-title h2 {
		margin: 0;
		font-size: var(--text-base);
		font-weight: 600;
	}

	.day-title p {
		margin: 2px 0 0;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.pills {
		display: flex;
		gap: 4px;
		flex-shrink: 0;
	}

	.pill {
		padding: 3px 7px;
		border: 1px solid var(--border);
		border-radius: 5px;
		color: var(--fg-muted);
		font-size: 9px;
		white-space: nowrap;
	}

	.pill--accent {
		border-color: var(--accent-border);
		background: var(--accent-surface);
		color: var(--accent-bright);
	}

	.day-progress {
		margin: 12px 0 4px;
	}

	.progress-label {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 5px;
		color: var(--fg-faint);
		font-size: 9px;
	}

	.progress-label strong {
		color: var(--fg-muted);
		font-weight: 550;
		font-variant-numeric: tabular-nums;
	}

	.progress {
		height: 3px;
		overflow: hidden;
		border-radius: 2px;
		background: var(--bg-raised);
	}

	.progress span {
		display: block;
		height: 100%;
		border-radius: inherit;
		background: var(--accent);
		transition: width var(--duration-normal) var(--ease-out);
	}

	.day-scroll {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
		padding-right: 2px;
		scrollbar-width: none;
	}

	.day-scroll::-webkit-scrollbar {
		width: 0;
		height: 0;
	}

	.group-title {
		display: flex;
		align-items: center;
		gap: 5px;
		margin: 12px 0 4px;
		color: var(--fg-faint);
		font-size: 9px;
		font-weight: 600;
		letter-spacing: 0.07em;
		text-transform: uppercase;
	}

	.group-title span {
		color: var(--fg-faint);
		font-family: var(--font-mono);
		font-size: 9px;
		font-weight: 400;
	}

	.task-list,
	.event-list {
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.event-list li {
		display: flex;
		align-items: flex-start;
		gap: 9px;
		padding: 7px 4px;
		border-radius: 6px;
	}

	.event-list li:hover {
		background: var(--bg-raised);
	}

	.event-list li.conflict {
		background: var(--warning-surface);
	}

	.event-open {
		display: flex;
		flex: 1;
		min-width: 0;
		gap: 9px;
		padding: 0;
		border: 0;
		background: none;
		color: inherit;
		font: inherit;
		text-align: left;
		cursor: pointer;
	}

	.event-open:hover .task-title {
		color: var(--fg-primary);
	}

	.event-list li.editing {
		background: var(--accent-surface);
	}

	.event-actions {
		display: flex;
		align-items: center;
		gap: 4px;
		margin-left: auto;
		opacity: 0;
		transition: opacity 160ms ease;
	}

	.event-list li:hover .event-actions,
	.event-list li:focus-within .event-actions,
	.event-list li.editing .event-actions {
		opacity: 1;
	}

	/* Touch devices have no hover: keep the actions permanently visible. */
	@media (hover: none) {
		.event-actions {
			opacity: 1;
		}
	}

	.event-action {
		display: inline-grid;
		place-items: center;
		height: 22px;
		min-width: 22px;
		padding: 0 6px;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--bg-raised);
		color: var(--fg-muted);
		font-size: var(--text-2xs);
		white-space: nowrap;
		cursor: pointer;
		transition:
			background-color 160ms ease,
			border-color 160ms ease,
			color 160ms ease;
	}

	.event-action:hover:not(:disabled) {
		border-color: var(--accent-border);
		color: var(--fg-primary);
	}

	.event-action--danger:hover:not(:disabled) {
		border-color: var(--danger-border);
		background: var(--danger-surface);
		color: var(--danger-text);
	}

	.event-editor {
		display: flex;
		flex-direction: column;
		flex-shrink: 0;
		gap: 8px;
		margin: 4px 0 10px;
		padding: 10px;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--bg-raised);
		animation: editor-in 180ms ease;
	}

	.editor-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}

	.editor-head h3 {
		margin: 0;
		color: var(--fg-primary);
		font-size: var(--text-sm);
		font-weight: 600;
	}

	.field-row {
		display: flex;
		gap: 8px;
	}

	.field {
		display: flex;
		flex: 1;
		min-width: 0;
		flex-direction: column;
		gap: 3px;
	}

	.field > span {
		color: var(--fg-muted);
		font-size: var(--text-2xs);
	}

	.field input,
	.field select,
	.field textarea {
		width: 100%;
		min-width: 0;
		padding: 5px 7px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--bg-card);
		color: var(--fg-body);
		font-family: inherit;
		font-size: var(--text-xs);
		transition:
			border-color 160ms ease,
			box-shadow 160ms ease;
	}

	.field textarea {
		resize: vertical;
	}

	.field input:focus-visible,
	.field select:focus-visible,
	.field textarea:focus-visible {
		border-color: var(--accent-border);
		outline: none;
		box-shadow: 0 0 0 2px var(--accent-surface);
	}

	.field--check {
		flex: 0 0 auto;
		flex-direction: row;
		align-items: center;
		gap: 6px;
		padding-top: 14px;
	}

	.field--check input {
		width: auto;
		accent-color: var(--accent);
	}

	.edit-error {
		margin: 0;
		color: var(--danger-text);
		font-size: var(--text-2xs);
	}

	.editor-actions {
		display: flex;
		align-items: center;
		gap: 6px;
	}

	.editor-spacer {
		flex: 1;
	}

	.editor-hint {
		margin: 0;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
	}

	.event-notice {
		display: flex;
		align-items: center;
		gap: 6px;
		flex-shrink: 0;
		margin: 0 0 8px;
		padding: 6px 9px;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--bg-raised);
		color: var(--fg-body);
		font-size: var(--text-2xs);
		animation: editor-in 160ms ease;
	}

	.notice-close {
		margin-left: auto;
		padding: 0;
		border: 0;
		background: none;
		color: var(--fg-muted);
		cursor: pointer;
	}

	@keyframes editor-in {
		from {
			opacity: 0;
			transform: translateY(-4px);
		}
		to {
			opacity: 1;
			transform: translateY(0);
		}
	}

	.event-time {
		width: 58px;
		padding-top: 1px;
		color: var(--fg-muted);
		font-size: var(--text-2xs);
		font-variant-numeric: tabular-nums;
		flex-shrink: 0;
	}

	.conflict-badge {
		padding: 2px 5px;
		border: 1px solid var(--warning-border);
		border-radius: 4px;
		color: var(--warning);
		font-size: 8px;
		white-space: nowrap;
	}

	.task-list li {
		display: flex;
		align-items: flex-start;
		gap: 9px;
		padding: 7px 4px;
		border-radius: 6px;
	}

	.task-list li:hover {
		background: var(--bg-raised);
	}

	.check {
		display: grid;
		place-items: center;
		width: 15px;
		height: 15px;
		margin-top: 1px;
		padding: 0;
		border: 1.4px solid var(--ink-700);
		border-radius: 4px;
		background: transparent;
		color: #fff;
		flex-shrink: 0;
	}

	.check:hover:not(:disabled) {
		border-color: var(--accent);
	}

	.check.checked {
		border-color: var(--accent);
		background: var(--accent);
	}

	.check:disabled {
		opacity: 0.5;
	}

	.task-copy {
		display: flex;
		min-width: 0;
		flex-direction: column;
		gap: 3px;
	}

	.task-title {
		color: var(--fg-body);
		font-size: var(--text-sm);
		line-height: 1.35;
	}

	.task-meta {
		display: flex;
		align-items: center;
		gap: 4px;
		color: var(--fg-faint);
		font-size: 9.5px;
	}

	.priority {
		display: inline-flex;
		align-items: center;
		gap: 4px;
	}

	.priority i {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--fg-faint);
	}

	.priority[data-priority='high'] {
		color: #fca5a5;
	}

	.priority[data-priority='high'] i {
		background: var(--danger);
	}

	.priority[data-priority='medium'] {
		color: var(--warning);
	}

	.priority[data-priority='medium'] i {
		background: var(--warning);
	}

	.priority[data-priority='low'] i {
		background: var(--info);
	}

	.task-list--done .task-title {
		color: var(--fg-faint);
		text-decoration: line-through;
	}

	.add-row {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 6px;
		width: 100%;
		height: 30px;
		margin-top: 10px;
		border: 1px dashed var(--border);
		border-radius: 6px;
		background: transparent;
		color: var(--fg-faint);
		font-size: var(--text-xs);
	}

	.add-row:hover {
		border-color: var(--accent-border);
		color: var(--accent-bright);
	}

	.day-empty {
		margin: 18px 0;
		color: var(--fg-faint);
		font-size: var(--text-sm);
		line-height: 1.55;
	}

	.day-skeleton {
		display: flex;
		flex-direction: column;
		gap: 12px;
		padding: 16px 0;
	}

	.skeleton-line {
		display: block;
		width: 100%;
		height: 10px;
		border-radius: var(--radius-full);
		background: linear-gradient(90deg, var(--bg-raised), var(--bg-hover), var(--bg-raised));
		background-size: 200% 100%;
		animation: shimmer 1.4s infinite linear;
	}

	.skeleton-line--medium {
		width: 62%;
	}

	.calendar-footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 16px;
		height: 38px;
		min-height: 38px;
		color: var(--fg-faint);
		font-size: 9.5px;
	}

	.footer-sync {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		color: var(--fg-muted);
	}

	.footer-sync :global(svg) {
		color: var(--accent-bright);
	}

	@keyframes shimmer {
		from {
			background-position: 200% 0;
		}
		to {
			background-position: -200% 0;
		}
	}

	@media (max-width: 1180px) {
		.calendar-grid {
			grid-template-columns: minmax(0, 1fr) 340px;
		}

		.legend {
			display: none;
		}
	}

	@media (max-width: 1020px) {
		.calendar-page {
			height: auto;
			min-height: calc(100dvh - var(--mobile-nav-h));
		}

		.calendar-grid {
			grid-template-columns: minmax(0, 1fr);
		}

		.calendar-card {
			min-height: 460px;
		}

		.day-card {
			max-height: 520px;
		}

		.calendar-footer {
			flex-wrap: wrap;
			height: auto;
			padding: 10px 0;
		}
	}

	@media (max-width: 820px) {
		.calendar-page {
			padding: 0 14px;
		}

		.page-head {
			height: auto;
			min-height: 56px;
			flex-wrap: wrap;
			padding: 8px 0;
		}

		.head-actions {
			width: 100%;
			flex-wrap: wrap;
		}

		.primary {
			margin-left: auto;
		}

		.calendar-card,
		.day-card {
			padding: 10px;
		}

		.month-grid {
			grid-auto-rows: minmax(54px, 1fr);
		}

		.event {
			padding: 2px;
			text-indent: -9999px;
			width: 5px;
			height: 5px;
			border-radius: 50%;
			background: var(--accent);
			overflow: hidden;
		}

		.event[data-tone='overdue'] {
			background: var(--danger);
		}

		.event[data-tone='done'] {
			background: var(--success);
		}

		.event[data-tone='info'] {
			background: var(--info);
		}

		.event[data-kind='event'][data-calendar='work'] {
			background: var(--accent);
		}

		.event[data-kind='event'][data-calendar='meetings'] {
			background: var(--success);
		}

		.event[data-kind='event'][data-calendar='personal'] {
			background: var(--info);
		}

		.event[data-conflict='true'] {
			box-shadow: 0 0 0 1px var(--warning);
		}

		.event::before {
			display: none;
		}

		.more {
			font-size: 9px;
		}

		.day-card {
			max-height: none;
		}

		.day-scroll {
			overflow: visible;
		}

		.create-bar {
			flex-wrap: wrap;
		}

		.create-bar input {
			flex-basis: 100%;
		}
	}

	@media (max-width: 560px) {
		.month-select {
			order: -1;
		}

		.segmented .today-button {
			padding-inline: 6px;
		}

		.head-actions {
			row-gap: 6px;
		}

		.primary {
			flex: 1 0 100%;
			justify-content: center;
		}

		.day-cell {
			padding: 4px;
		}

		.weekday-row {
			font-size: 8px;
		}

		.pills {
			flex-direction: column;
		}

		.calendar-footer {
			font-size: 9px;
		}
	}
</style>
