<script lang="ts">
	import { onMount } from 'svelte';
	import TaskRow from '$lib/components/TaskRow.svelte';
	import {
		cancelTask,
		completeTask,
		createTask,
		listTasks,
		rescheduleTask,
		restoreTask,
		type Priority,
		type Status,
		type Task
	} from '$lib/api';
	import { errorMessage, todayISO } from '$lib/format';
	import { liveRevision } from '$lib/live';

	const limit = 20;

	let tasks: Task[] = [];
	let total = 0;
	let offset = 0;
	let loading = true;
	let error = '';
	let busyId: number | null = null;

	let status: '' | Status = '';
	let priority: '' | Priority = '';
	let search = '';
	let sort = '-created_at';

	let showCreate = false;
	let newTitle = '';
	let newPriority: Priority = 'medium';
	let newDue = '';
	let creating = false;

	async function load() {
		loading = true;
		try {
			const result = await listTasks({
				status: status ? [status] : [],
				priority: priority ? [priority] : [],
				search: search.trim() || undefined,
				sort,
				limit,
				offset
			});
			tasks = result.data;
			total = result.meta.total;
			error = '';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			loading = false;
		}
	}

	function applyFilters() {
		offset = 0;
		load();
	}

	async function run(id: number, action: () => Promise<unknown>) {
		busyId = id;
		try {
			await action();
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	const complete = (task: Task) => run(task.id, () => completeTask(task.id, task.version));
	const restore = (task: Task) => run(task.id, () => restoreTask(task.id, task.version));
	const cancel = (task: Task) => run(task.id, () => cancelTask(task.id, task.version));
	const reschedule = (task: Task, date: string) =>
		run(task.id, () => rescheduleTask(task.id, task.version, { due_date: date }));

	async function create() {
		const title = newTitle.trim();
		if (!title || creating) return;
		creating = true;
		try {
			await createTask({
				title,
				priority: newPriority,
				due_date: newDue || undefined
			});
			newTitle = '';
			newDue = '';
			newPriority = 'medium';
			showCreate = false;
			offset = 0;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			creating = false;
		}
	}

	function nextPage() {
		if (offset + limit < total) {
			offset += limit;
			load();
		}
	}

	function prevPage() {
		if (offset > 0) {
			offset = Math.max(0, offset - limit);
			load();
		}
	}

	onMount(() => {
		load();
		let timer: ReturnType<typeof setTimeout> | undefined;
		const unsubscribe = liveRevision.subscribe((revision) => {
			if (revision === 0) return;
			clearTimeout(timer);
			timer = setTimeout(load, 200);
		});
		return () => {
			unsubscribe();
			clearTimeout(timer);
		};
	});
</script>

<svelte:head>
	<title>Задачи — LifeOS</title>
</svelte:head>

<header class="page-header">
	<div>
		<h1>Все задачи</h1>
		<p class="subtitle">{total} всего · показано {tasks.length}</p>
	</div>
	<button class="primary" on:click={() => (showCreate = !showCreate)}>
		{showCreate ? 'Скрыть форму' : 'Новая задача'}
	</button>
</header>

{#if error}
	<div class="banner" role="alert">
		<span>{error}</span>
		<button on:click={() => (error = '')} aria-label="Закрыть">×</button>
	</div>
{/if}

{#if showCreate}
	<form class="create" on:submit|preventDefault={create}>
		<input bind:value={newTitle} placeholder="Что нужно сделать?" aria-label="Название задачи" autocomplete="off" />
		<select bind:value={newPriority} aria-label="Приоритет">
			<option value="high">Высокий</option>
			<option value="medium">Средний</option>
			<option value="low">Низкий</option>
		</select>
		<input type="date" bind:value={newDue} aria-label="Дедлайн" min={todayISO()} />
		<button type="submit" disabled={creating || !newTitle.trim()}>{creating ? 'Создаю…' : 'Создать'}</button>
	</form>
{/if}

<div class="filters">
	<input
		class="search"
		bind:value={search}
		on:input={applyFilters}
		placeholder="Поиск по названию и заметке"
		aria-label="Поиск задач"
	/>
	<select bind:value={status} on:change={applyFilters} aria-label="Статус">
		<option value="">Все статусы</option>
		<option value="open">Активные</option>
		<option value="completed">Выполненные</option>
		<option value="cancelled">Отменённые</option>
	</select>
	<select bind:value={priority} on:change={applyFilters} aria-label="Приоритет">
		<option value="">Все приоритеты</option>
		<option value="high">Высокий</option>
		<option value="medium">Средний</option>
		<option value="low">Низкий</option>
	</select>
	<select bind:value={sort} on:change={applyFilters} aria-label="Сортировка">
		<option value="-created_at">Сначала новые</option>
		<option value="created_at">Сначала старые</option>
		<option value="due_at">По дедлайну</option>
		<option value="-priority">По приоритету</option>
		<option value="title">По названию</option>
	</select>
</div>

{#if loading && tasks.length === 0}
	<p class="muted">Загружаю…</p>
{:else if tasks.length === 0}
	<div class="empty">
		<p>Задач не найдено.</p>
		<p class="muted">Измените фильтры или создайте новую задачу.</p>
	</div>
{:else}
	<div class="list">
		{#each tasks as task (task.id)}
			<TaskRow
				{task}
				busy={busyId === task.id}
				oncomplete={complete}
				onrestore={restore}
				oncancel={cancel}
				onreschedule={reschedule}
			/>
		{/each}
	</div>

	<div class="pager">
		<button class="ghost" disabled={offset === 0 || loading} on:click={prevPage}>Назад</button>
		<span class="muted">{offset + 1}–{Math.min(offset + limit, total)} из {total}</span>
		<button class="ghost" disabled={offset + limit >= total || loading} on:click={nextPage}>Вперёд</button>
	</div>
{/if}

<style>
	.page-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 1rem;
		margin-bottom: 1.25rem;
	}

	h1 {
		margin: 0;
		font-size: 1.9rem;
		letter-spacing: -0.02em;
	}

	.subtitle {
		margin: 0.25rem 0 0;
		color: var(--fg-muted);
		font-size: 0.9rem;
	}

	.primary {
		background: linear-gradient(135deg, #8b5cf6, #06b6d4);
		color: #0a0a0f;
		border: none;
		border-radius: 10px;
		padding: 0.6rem 0.95rem;
		font-weight: 600;
		cursor: pointer;
	}

	.banner {
		display: flex;
		justify-content: space-between;
		align-items: center;
		gap: 1rem;
		padding: 0.7rem 0.9rem;
		margin-bottom: 1rem;
		border: 1px solid rgba(248, 113, 113, 0.4);
		background: rgba(248, 113, 113, 0.08);
		color: #fca5a5;
		border-radius: 10px;
		font-size: 0.85rem;
	}

	.banner button {
		background: none;
		border: none;
		color: inherit;
		font-size: 1.1rem;
		cursor: pointer;
	}

	.create {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto auto auto;
		gap: 0.5rem;
		padding: 0.9rem;
		border: 1px solid var(--border);
		border-radius: 12px;
		background: var(--bg-card);
		margin-bottom: 1rem;
	}

	.create input,
	.create select {
		padding: 0.55rem 0.7rem;
		border-radius: 8px;
	}

	.create button {
		background: #8b5cf6;
		color: #0a0a0f;
		border: none;
		border-radius: 8px;
		padding: 0.55rem 0.9rem;
		font-weight: 600;
		cursor: pointer;
	}

	.create button:disabled {
		opacity: 0.55;
		cursor: default;
	}

	.filters {
		display: grid;
		grid-template-columns: minmax(0, 1fr) repeat(3, auto);
		gap: 0.5rem;
		margin-bottom: 1rem;
	}

	.filters input,
	.filters select {
		padding: 0.5rem 0.65rem;
		border-radius: 8px;
		font-size: 0.82rem;
	}

	.list {
		display: flex;
		flex-direction: column;
		gap: 0.55rem;
	}

	.pager {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 1rem;
		margin-top: 1.25rem;
	}

	.ghost {
		background: transparent;
		border: 1px solid var(--border);
		color: var(--fg-muted);
		border-radius: 8px;
		padding: 0.4rem 0.7rem;
		font-size: 0.8rem;
		cursor: pointer;
	}

	.ghost:disabled {
		opacity: 0.45;
		cursor: default;
	}

	.empty {
		padding: 2rem 1rem;
		border: 1px dashed var(--border);
		border-radius: 12px;
		text-align: center;
	}

	.empty p {
		margin: 0.2rem 0;
	}

	.muted {
		color: var(--fg-muted);
		font-size: 0.85rem;
	}

	@media (max-width: 720px) {
		.create,
		.filters {
			grid-template-columns: 1fr;
		}
	}
</style>
