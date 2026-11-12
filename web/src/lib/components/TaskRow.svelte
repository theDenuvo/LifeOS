<script lang="ts">
	import type { Task } from '$lib/api';
	import { formatDate, formatTime, priorityLabel, statusLabel } from '$lib/format';

	export let task: Task;
	export let busy = false;
	export let oncomplete: (task: Task) => void = () => {};
	export let onrestore: (task: Task) => void = () => {};
	export let oncancel: (task: Task) => void = () => {};
	export let onreschedule: (task: Task, date: string) => void = () => {};

	let date = task.due_date ?? '';
	$: date = task.due_date ?? '';
</script>

<article class="row" class:done={task.status === 'completed'} class:cancelled={task.status === 'cancelled'}>
	<button
		class="check"
		class:checked={task.status === 'completed'}
		aria-label={task.status === 'completed' ? 'Восстановить задачу' : 'Завершить задачу'}
		aria-pressed={task.status === 'completed'}
		disabled={busy}
		on:click={() => (task.status === 'completed' ? onrestore(task) : oncomplete(task))}
	>
		{#if task.status === 'completed'}
			<svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
				<path d="M3 8.5l3 3 7-7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
			</svg>
		{/if}
	</button>

	<div class="body">
		<div class="title-line">
			<h3>{task.title}</h3>
			{#if task.overdue}
				<span class="badge danger">Просрочена</span>
			{:else if task.status !== 'open'}
				<span class="badge">{statusLabel(task.status)}</span>
			{/if}
		</div>

		{#if task.note}
			<p class="note">{task.note}</p>
		{/if}

		<div class="meta">
			<span class="priority" data-priority={task.priority}>
				<span class="dot" aria-hidden="true"></span>
				{priorityLabel(task.priority)}
			</span>
			{#if task.due_at}
				<span>{formatDate(task.due_at)} · {formatTime(task.due_at)}</span>
			{:else if task.due_date}
				<span>до {formatDate(task.due_date)}</span>
			{:else if task.planned_date}
				<span>план {formatDate(task.planned_date)}</span>
			{:else}
				<span>без срока</span>
			{/if}
			{#each task.tags as tag}
				<span class="tag">#{tag}</span>
			{/each}
		</div>
	</div>

	<div class="actions">
		{#if task.status === 'open'}
			<label class="reschedule">
				<span class="sr-only">Новая дата</span>
				<input type="date" bind:value={date} disabled={busy} />
			</label>
			<button class="ghost" disabled={busy || !date} on:click={() => onreschedule(task, date)}>
				Перенести
			</button>
			<button class="ghost danger-text" disabled={busy} on:click={() => oncancel(task)}>Отменить</button>
		{:else}
			<button class="ghost" disabled={busy} on:click={() => onrestore(task)}>Вернуть</button>
		{/if}
	</div>
</article>

<style>
	.row {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) auto;
		gap: 0.9rem;
		align-items: start;
		padding: 0.9rem 1rem;
		border: 1px solid var(--border);
		border-radius: 12px;
		background: var(--bg-card);
	}

	.row.done {
		opacity: 0.62;
	}

	.row.cancelled {
		opacity: 0.5;
	}

	.check {
		width: 22px;
		height: 22px;
		margin-top: 0.15rem;
		border-radius: 7px;
		border: 1.5px solid var(--border);
		background: transparent;
		color: #0a0a0f;
		display: grid;
		place-items: center;
		cursor: pointer;
		padding: 0;
	}

	.check.checked {
		background: #22c55e;
		border-color: #22c55e;
	}

	.check svg {
		width: 14px;
		height: 14px;
	}

	.title-line {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		flex-wrap: wrap;
	}

	h3 {
		margin: 0;
		font-size: 0.98rem;
		font-weight: 550;
		line-height: 1.35;
	}

	.done h3 {
		text-decoration: line-through;
	}

	.note {
		margin: 0.3rem 0 0;
		color: var(--fg-muted);
		font-size: 0.84rem;
		line-height: 1.45;
		white-space: pre-wrap;
	}

	.meta {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.55rem;
		margin-top: 0.45rem;
		font-size: 0.76rem;
		color: var(--fg-muted);
	}

	.priority {
		display: inline-flex;
		align-items: center;
		gap: 0.35rem;
	}

	.dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: #71717a;
	}

	.priority[data-priority='high'] .dot {
		background: #f87171;
	}

	.priority[data-priority='medium'] .dot {
		background: #fbbf24;
	}

	.priority[data-priority='low'] .dot {
		background: #38bdf8;
	}

	.tag {
		color: #a5b4fc;
	}

	.badge {
		font-size: 0.68rem;
		padding: 0.12rem 0.45rem;
		border-radius: 999px;
		border: 1px solid var(--border);
		color: var(--fg-muted);
	}

	.badge.danger {
		border-color: rgba(248, 113, 113, 0.4);
		color: #fca5a5;
		background: rgba(248, 113, 113, 0.08);
	}

	.actions {
		display: flex;
		align-items: center;
		gap: 0.35rem;
		flex-wrap: wrap;
		justify-content: flex-end;
	}

	.reschedule input {
		padding: 0.3rem 0.4rem;
		font-size: 0.75rem;
		border-radius: 7px;
	}

	.ghost {
		background: transparent;
		border: 1px solid var(--border);
		color: var(--fg-muted);
		border-radius: 7px;
		padding: 0.3rem 0.55rem;
		font-size: 0.75rem;
		cursor: pointer;
	}

	.ghost:hover:not(:disabled) {
		color: var(--fg-primary);
		border-color: #4c4c63;
	}

	.ghost:disabled {
		opacity: 0.5;
		cursor: default;
	}

	.danger-text:hover:not(:disabled) {
		color: #fca5a5;
		border-color: rgba(248, 113, 113, 0.4);
	}

	.sr-only {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip: rect(0 0 0 0);
	}

	@media (max-width: 640px) {
		.row {
			grid-template-columns: auto minmax(0, 1fr);
		}

		.actions {
			grid-column: 1 / -1;
			justify-content: flex-start;
		}
	}
</style>
