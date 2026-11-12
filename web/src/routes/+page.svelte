<script lang="ts">
	import { onMount } from 'svelte';
	import {
		captureInbox,
		completeTask,
		dismissInsight,
		getInsights,
		getSummary,
		getTask,
		snoozeInsight,
		type Insight,
		type Summary
	} from '$lib/api';
	import { errorMessage, formatLongDate, priorityLabel } from '$lib/format';
	import { liveRevision } from '$lib/live';

	let summary: Summary | null = null;
	let insights: Insight[] = [];
	let loading = true;
	let error = '';
	let captureText = '';
	let capturing = false;
	let busyId: number | null = null;
	$: focus = summary?.focus_task ?? null;

	async function load() {
		loading = true;
		try {
			const [nextSummary, nextInsights] = await Promise.all([getSummary(), getInsights()]);
			summary = nextSummary;
			insights = nextInsights.data;
			error = '';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			loading = false;
		}
	}

	async function complete(id: number) {
		busyId = id;
		try {
			const task = await getTask(id);
			await completeTask(task.id, task.version);
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function hideInsight(key: string) {
		try {
			await dismissInsight(key);
			insights = insights.filter((insight) => insight.key !== key);
		} catch (err) {
			error = errorMessage(err);
		}
	}

	async function remindTomorrow(key: string) {
		try {
			const until = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
			await snoozeInsight(key, until);
			insights = insights.filter((insight) => insight.key !== key);
		} catch (err) {
			error = errorMessage(err);
		}
	}

	async function capture() {
		const text = captureText.trim();
		if (!text || capturing) return;
		capturing = true;
		try {
			await captureInbox(text);
			captureText = '';
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			capturing = false;
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
	<title>Сегодня — LifeOS</title>
</svelte:head>

<header class="page-header">
	<div>
		<h1>Сегодня</h1>
		<p class="subtitle">{formatLongDate(new Date())}</p>
	</div>
	<button class="refresh" on:click={load} disabled={loading} aria-label="Обновить">Обновить</button>
</header>

{#if error}
	<div class="banner" role="alert">
		<span>{error}</span>
		<button on:click={() => (error = '')} aria-label="Закрыть">×</button>
	</div>
{/if}

<form class="capture" on:submit|preventDefault={capture}>
	<input
		bind:value={captureText}
		placeholder="Быстрый ввод: задача, мысль, ссылка…"
		aria-label="Быстрый ввод во входящие"
		autocomplete="off"
	/>
	<button type="submit" disabled={capturing || !captureText.trim()}>
		{capturing ? 'Сохраняю…' : 'Во входящие'}
	</button>
</form>

{#if insights.length > 0}
	<section class="insights" aria-label="Наблюдения">
		{#each insights as insight (insight.key)}
			<article class="insight" data-severity={insight.severity}>
				<div class="insight-main">
					<span class="insight-title">{insight.title}</span>
					<p class="insight-detail">{insight.detail}</p>
				</div>
				<div class="insight-actions">
					<button class="ghost" on:click={() => remindTomorrow(insight.key)}>Завтра</button>
					<button class="ghost" on:click={() => hideInsight(insight.key)}>Скрыть</button>
				</div>
			</article>
		{/each}
	</section>
{/if}

{#if loading && !summary}
	<p class="muted">Загружаю…</p>
{:else if summary}
	<section class="stats" aria-label="Статистика дня">
		<div class="stat">
			<span class="value">{summary.stats.active_total}</span>
			<span class="label">активных</span>
		</div>
		<div class="stat danger">
			<span class="value">{summary.stats.overdue_count}</span>
			<span class="label">просрочено</span>
		</div>
		<div class="stat warning">
			<span class="value">{summary.stats.high_priority_count}</span>
			<span class="label">высокий приоритет</span>
		</div>
		<div class="stat success">
			<span class="value">{summary.stats.completed_today}</span>
			<span class="label">выполнено сегодня</span>
		</div>
		<div class="stat">
			<span class="value">{summary.stats.inbox_count}</span>
			<span class="label">во входящих</span>
		</div>
	</section>

	{#if focus}
		<section class="focus">
			<div class="focus-head">
				<span class="focus-badge">Фокус</span>
				<span class="focus-priority">{priorityLabel(focus.priority)}</span>
			</div>
			<h2>{focus.title}</h2>
			<p class="muted">{focus.time === '—' ? 'Без точного времени' : `Сегодня в ${focus.time}`}</p>
			<button class="primary" disabled={busyId === focus.id} on:click={() => complete(focus.id)}>
				Завершить
			</button>
		</section>
	{/if}

	<div class="columns">
		<section>
			<h2 class="section-title">Задачи на сегодня</h2>
			{#if summary.today.length === 0}
				<p class="empty">На сегодня задач нет. Добавьте через быстрый ввод или раздел «Задачи».</p>
			{:else}
				<ul class="list">
					{#each summary.today as item}
						<li>
							<div class="item-main">
								<span class="item-title">{item.title}</span>
								<span class="item-meta">{item.time === '—' ? 'без времени' : item.time} · {priorityLabel(item.priority)}</span>
							</div>
							<button class="ghost" disabled={busyId === item.id} on:click={() => complete(item.id)}>Готово</button>
						</li>
					{/each}
				</ul>
			{/if}
		</section>

		<section>
			<h2 class="section-title">Просроченные</h2>
			{#if summary.overdue.length === 0}
				<p class="empty">Просроченных задач нет.</p>
			{:else}
				<ul class="list">
					{#each summary.overdue as item}
						<li>
							<div class="item-main">
								<span class="item-title">{item.title}</span>
								<span class="item-meta danger-text">
									{item.days_overdue === 1 ? '1 день' : `${item.days_overdue} дн.`} · {priorityLabel(item.priority)}
								</span>
							</div>
							<button class="ghost" disabled={busyId === item.id} on:click={() => complete(item.id)}>Готово</button>
						</li>
					{/each}
				</ul>
			{/if}
		</section>
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

	.refresh {
		background: transparent;
		border: 1px solid var(--border);
		color: var(--fg-muted);
		border-radius: 8px;
		padding: 0.4rem 0.7rem;
		font-size: 0.8rem;
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

	.capture {
		display: flex;
		gap: 0.5rem;
		margin-bottom: 1.5rem;
	}

	.capture input {
		flex: 1;
		padding: 0.7rem 0.9rem;
		border-radius: 10px;
	}

	.capture button,
	.primary {
		background: linear-gradient(135deg, #8b5cf6, #06b6d4);
		color: #0a0a0f;
		border: none;
		border-radius: 10px;
		padding: 0.7rem 1rem;
		font-weight: 600;
		cursor: pointer;
	}

	.capture button:disabled,
	.primary:disabled {
		opacity: 0.55;
		cursor: default;
	}

	.insights {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		margin-bottom: 1.25rem;
	}

	.insight {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
		padding: 0.7rem 0.9rem;
		border: 1px solid var(--border);
		border-left: 3px solid #71717a;
		border-radius: 10px;
		background: var(--bg-card);
	}

	.insight[data-severity='critical'] {
		border-left-color: #f87171;
	}

	.insight[data-severity='warning'] {
		border-left-color: #fbbf24;
	}

	.insight[data-severity='info'] {
		border-left-color: #38bdf8;
	}

	.insight-title {
		font-size: 0.88rem;
		font-weight: 550;
	}

	.insight-detail {
		margin: 0.15rem 0 0;
		font-size: 0.78rem;
		color: var(--fg-muted);
	}

	.insight-actions {
		display: flex;
		gap: 0.35rem;
		flex-shrink: 0;
	}

	.stats {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
		gap: 0.6rem;
		margin-bottom: 1.5rem;
	}

	.stat {
		display: flex;
		flex-direction: column;
		gap: 0.15rem;
		padding: 0.8rem 0.9rem;
		border: 1px solid var(--border);
		border-radius: 12px;
		background: var(--bg-card);
	}

	.stat .value {
		font-size: 1.4rem;
		font-weight: 650;
	}

	.stat .label {
		font-size: 0.72rem;
		color: var(--fg-muted);
	}

	.stat.danger .value {
		color: #fca5a5;
	}

	.stat.warning .value {
		color: #fcd34d;
	}

	.stat.success .value {
		color: #86efac;
	}

	.focus {
		padding: 1.1rem 1.2rem;
		border-radius: 14px;
		border: 1px solid rgba(139, 92, 246, 0.35);
		background: radial-gradient(120% 140% at 0% 0%, rgba(139, 92, 246, 0.18), rgba(18, 18, 26, 0.9));
		margin-bottom: 1.75rem;
	}

	.focus-head {
		display: flex;
		align-items: center;
		gap: 0.6rem;
		margin-bottom: 0.4rem;
	}

	.focus-badge {
		font-size: 0.68rem;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		color: #c4b5fd;
	}

	.focus-priority {
		font-size: 0.72rem;
		color: var(--fg-muted);
	}

	.focus h2 {
		margin: 0 0 0.3rem;
		font-size: 1.15rem;
	}

	.focus .primary {
		margin-top: 0.8rem;
		padding: 0.5rem 0.9rem;
		font-size: 0.85rem;
	}

	.columns {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
		gap: 1.5rem;
	}

	.section-title {
		font-size: 0.8rem;
		text-transform: uppercase;
		letter-spacing: 0.08em;
		color: var(--fg-muted);
		margin: 0 0 0.6rem;
	}

	.list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}

	.list li {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		padding: 0.7rem 0.85rem;
		border: 1px solid var(--border);
		border-radius: 10px;
		background: var(--bg-card);
	}

	.item-main {
		display: flex;
		flex-direction: column;
		gap: 0.15rem;
		min-width: 0;
	}

	.item-title {
		font-size: 0.9rem;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.item-meta {
		font-size: 0.74rem;
		color: var(--fg-muted);
	}

	.ghost {
		background: transparent;
		border: 1px solid var(--border);
		color: var(--fg-muted);
		border-radius: 7px;
		padding: 0.3rem 0.55rem;
		font-size: 0.75rem;
		cursor: pointer;
		flex-shrink: 0;
	}

	.ghost:hover:not(:disabled) {
		color: var(--fg-primary);
	}

	.empty,
	.muted {
		color: var(--fg-muted);
		font-size: 0.85rem;
	}

	.danger-text {
		color: #fca5a5;
	}
</style>
