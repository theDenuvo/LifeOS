<script lang="ts">
	import { onMount } from 'svelte';
	import {
		applyPlan,
		captureInbox,
		completeTask,
		convertInbox,
		deletePlanBlock,
		dismissInsight,
		getDayPlan,
		getInsights,
		getPlanProposal,
		getSummary,
		getTask,
		snoozeInsight,
		updateInbox,
		type DayPlan,
		type Insight,
		type PlanItem,
		type PlanProposal,
		type Summary
	} from '$lib/api';
	import { errorMessage, formatLongDate, priorityLabel, todayISO } from '$lib/format';
	import { liveRevision } from '$lib/live';

	let summary: Summary | null = null;
	let insights: Insight[] = [];
	let loading = true;
	let error = '';
	let captureText = '';
	let capturing = false;
	let busyId: number | null = null;
	$: focus = summary?.focus_task ?? null;

	// --- Stage 1: the day plan -------------------------------------------------
	let plan: DayPlan | null = null;
	let planLoading = false;
	let planApplying = false;
	let planNotice = '';

	// --- Stage 1: replanning proposal -----------------------------------------
	let proposal: PlanProposal | null = null;
	let proposalLoading = false;
	let proposalApplying = false;

	// --- Stage 1: ideas coming back -------------------------------------------
	let revisitingId: number | null = null;

	async function load() {
		loading = true;
		try {
			const [nextSummary, nextInsights, nextPlan] = await Promise.all([
				getSummary(),
				getInsights(),
				getDayPlan(todayISO()).catch(() => null)
			]);
			summary = nextSummary;
			insights = nextInsights.data;
			plan = nextPlan;
			error = '';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			loading = false;
		}
	}

	/**
	 * Explains where a duration came from, so the plan never shows an unexplained
	 * number: the user's own estimate, a median of similar work, or the default.
	 */
	function estimateSourceHint(source?: string): string {
		if (source === 'history') return ' · по истории';
		if (source === 'default') return ' · по умолчанию';
		return '';
	}

	/** Accepts the planner's suggestion: only the proposed blocks are written. */
	async function acceptPlan() {
		if (!plan || plan.proposed.length === 0 || planApplying) return;
		planApplying = true;
		try {
			const items = plan.proposed.map((item) => ({
				task_id: item.task_id,
				start: item.start,
				end: item.end
			}));
			const result = await applyPlan([plan.date], items);
			planNotice = `Запланировано блоков: ${result.applied.length}`;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			planApplying = false;
		}
	}

	/** Removes reserved time; the task itself stays open. */
	async function dropBlock(item: PlanItem) {
		if (!item.block_id) return;
		try {
			await deletePlanBlock(item.block_id);
			await load();
		} catch (err) {
			error = errorMessage(err);
		}
	}

	async function loadProposal() {
		proposalLoading = true;
		try {
			proposal = await getPlanProposal(7);
		} catch (err) {
			error = errorMessage(err);
		} finally {
			proposalLoading = false;
		}
	}

	/** Applies the accepted moves: the deadline stays, the time is re-reserved. */
	async function applyProposal() {
		if (!proposal || proposal.placements.length === 0 || proposalApplying) return;
		proposalApplying = true;
		try {
			const dates = Array.from(new Set(proposal.placements.map((item) => item.date)));
			const result = await applyPlan(
				dates,
				proposal.placements.map((item) => ({ task_id: item.task_id, start: item.start, end: item.end }))
			);
			planNotice = `Перенесено: ${result.applied.length}`;
			proposal = null;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			proposalApplying = false;
		}
	}

	/** Keeps an idea for later instead of forcing it into a task. */
	async function postponeIdea(id: number) {
		revisitingId = id;
		try {
			const date = new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
			await updateInbox(id, { revisit_at: date });
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			revisitingId = null;
		}
	}

	async function ideaToTask(id: number) {
		revisitingId = id;
		try {
			await convertInbox(id);
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			revisitingId = null;
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

{#if planNotice}
	<div class="banner ok" role="status">
		<span>{planNotice}</span>
		<button on:click={() => (planNotice = '')} aria-label="Закрыть">×</button>
	</div>
{/if}

{#if plan}
	<section class="plan" aria-label="План дня">
		<header class="plan-head">
			<div>
				<h2 class="section-title">План дня</h2>
				<p class="plan-summary">
					{#if plan.past}
						Этот день уже прошёл — свободного времени в нём не осталось
					{:else if plan.working}
						Рабочее время {plan.workday_start}–{plan.workday_end} · свободно {Math.floor(plan.free_minutes / 60)} ч {plan.free_minutes % 60} мин
						{#if plan.planned_minutes > 0}
							· занято под задачи {Math.floor(plan.planned_minutes / 60)} ч {plan.planned_minutes % 60} мин
						{/if}
					{:else}
						Сегодня нерабочий день — планировать нечего
					{/if}
				</p>
			</div>
			{#if plan.proposed.length > 0}
				<button class="primary" disabled={planApplying} on:click={acceptPlan}>
					{planApplying ? 'Записываю…' : `Принять план (${plan.proposed.length})`}
				</button>
			{/if}
		</header>

		{#if plan.blocks.length > 0 || plan.busy.length > 0}
			<ul class="timeline">
				{#each plan.blocks as item (item.block_id)}
					<li class="slot block" data-source={item.source}>
						<span class="time">{item.start_local}–{item.end_local}</span>
						<span class="slot-title">{item.title || 'Задача'}</span>
						{#if item.source === 'auto'}
							<button class="ghost tiny" on:click={() => dropBlock(item)} aria-label="Снять блок">
								Снять
							</button>
						{/if}
					</li>
				{/each}
				{#each plan.busy.filter((item) => item.kind === 'event') as item}
					<li class="slot busy">
						<span class="time">{new Date(item.start).toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })}</span>
						<span class="slot-title">{item.label}</span>
						<span class="slot-kind">встреча</span>
					</li>
				{/each}
			</ul>
		{:else if plan.working}
			<p class="empty">На этот день ещё ничего не стоит.</p>
		{/if}

		{#if plan.proposed.length > 0}
			<div class="proposal">
				<span class="proposal-title">Предложение планировщика</span>
				<ul class="timeline">
					{#each plan.proposed as item}
						<li class="slot proposed">
							<span class="time">{item.start_local}–{item.end_local}</span>
							<span class="slot-title">{item.title}</span>
							{#if item.estimate_minutes}
								<span class="slot-kind">
									{item.estimate_minutes} мин{estimateSourceHint(item.estimate_source)}
								</span>
							{/if}
						</li>
					{/each}
				</ul>
			</div>
		{/if}

		{#if plan.blocks.some((item) => item.source !== 'auto')}
			<p class="plan-hint">Блоки, поставленные вручную, планировщик не двигает.</p>
		{/if}

		{#if plan.unplaced.length > 0}
			<div class="unplaced">
				<span class="proposal-title">Не помещается</span>
				<ul>
					{#each plan.unplaced as item}
						<li>
							<span class="slot-title">{item.title}</span>
							<span class="slot-kind">{item.reason}</span>
						</li>
					{/each}
				</ul>
			</div>
		{/if}
	</section>
{/if}

{#if summary && (summary.overdue.length > 0 || plan?.unplaced.length)}
	<section class="replan" aria-label="Перенос просроченного">
		<div class="replan-head">
			<div>
				<h2 class="section-title">Есть просроченное</h2>
				<p class="plan-summary">Предложу, куда перенести дела, и ничего не изменю без вашего согласия.</p>
			</div>
			{#if !proposal}
				<button class="ghost" disabled={proposalLoading} on:click={loadProposal}>
					{proposalLoading ? 'Считаю…' : 'Предложить перенос'}
				</button>
			{/if}
		</div>

		{#if proposal}
			<p class="plan-summary">{proposal.summary}</p>
			{#if proposal.already_placed.length > 0}
				<div class="unplaced">
					<span class="proposal-title">Уже стоит в расписании</span>
					<ul>
						{#each proposal.already_placed as item}
							<li>
								<span class="slot-title">{item.title}</span>
								<span class="slot-kind">{item.reason}</span>
							</li>
						{/each}
					</ul>
				</div>
			{/if}
			{#if proposal.moves.length > 0}
				<ul class="moves">
					{#each proposal.moves as move}
						<li>
							<span class="slot-title">{move.title}</span>
							<span class="move-to">
								{move.from ? `${move.from} → ` : ''}{move.to} · {move.reason}
							</span>
						</li>
					{/each}
				</ul>
			{/if}
			<div class="replan-actions">
				{#if proposal.placements.length > 0}
					<button class="primary" disabled={proposalApplying} on:click={applyProposal}>
						{proposalApplying ? 'Применяю…' : 'Применить перенос'}
					</button>
				{/if}
				<button class="ghost" on:click={() => (proposal = null)}>Отмена</button>
			</div>
		{/if}
	</section>
{/if}

{#if summary && summary.revisit.length > 0}
	<section class="revisit" aria-label="Вернуться к идеям">
		<h2 class="section-title">Вернуться к</h2>
		<ul class="list">
			{#each summary.revisit as item (item.id)}
				<li>
					<div class="item-main">
						<span class="item-title">{item.text}</span>
						<span class="item-meta">{item.kind === 'idea' ? 'идея' : 'заметка'} · с {item.revisit_at}</span>
					</div>
					<div class="revisit-actions">
						<button class="ghost" disabled={revisitingId === item.id} on:click={() => ideaToTask(item.id)}>
							В задачи
						</button>
						<button class="ghost" disabled={revisitingId === item.id} on:click={() => postponeIdea(item.id)}>
							Позже
						</button>
					</div>
				</li>
			{/each}
		</ul>
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
		font-size: var(--text-2xl);
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
		background: var(--accent);
		color: var(--on-accent);
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

	.banner.ok {
		border-color: var(--success-surface);
		background: var(--success-surface);
		color: var(--success);
	}

	.plan-hint {
		margin: var(--space-2) 0 0;
		color: var(--text-muted);
		font-size: var(--text-sm);
	}

	.plan,
	.replan,
	.revisit {
		border: 1px solid var(--border);
		background: var(--bg-raised);
		border-radius: var(--radius-md);
		padding: var(--space-4);
		margin-bottom: var(--space-4);
		animation: card-in var(--duration-normal) var(--ease-out);
	}

	.plan-head,
	.replan-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-4);
		margin-bottom: var(--space-3);
	}

	.plan-summary {
		margin: var(--space-1) 0 0;
		color: var(--fg-muted);
		font-size: var(--text-sm);
	}

	.timeline {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}

	.slot {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius-sm);
		background: var(--bg-card);
		border-left: 2px solid var(--accent-border);
		font-size: var(--text-base);
	}

	.slot.busy {
		border-left-color: var(--border);
		color: var(--fg-muted);
	}

	.slot.proposed {
		border-left-color: var(--accent);
		border-left-style: dashed;
		background: var(--accent-surface);
	}

	.time {
		font-variant-numeric: tabular-nums;
		color: var(--fg-muted);
		font-size: var(--text-sm);
		min-width: 5.5rem;
	}

	.slot-title {
		font-weight: 500;
	}

	.slot-kind {
		margin-left: auto;
		color: var(--fg-faint);
		font-size: var(--text-xs);
	}

	.ghost.tiny {
		margin-left: auto;
		font-size: var(--text-xs);
		padding: 0.2rem 0.5rem;
	}

	.proposal,
	.unplaced {
		margin-top: var(--space-3);
	}

	.proposal-title {
		display: block;
		color: var(--fg-muted);
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		margin-bottom: var(--space-2);
	}

	.unplaced ul,
	.moves {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}

	.unplaced li,
	.moves li {
		display: flex;
		align-items: baseline;
		gap: var(--space-3);
		padding: var(--space-2) var(--space-3);
		border-radius: var(--radius-sm);
		background: var(--bg-card);
		font-size: var(--text-base);
	}

	.unplaced li .slot-kind,
	.move-to {
		margin-left: auto;
		color: var(--fg-faint);
		font-size: var(--text-xs);
	}

	.replan-actions {
		display: flex;
		gap: var(--space-2);
		margin-top: var(--space-3);
	}

	.revisit-actions {
		display: flex;
		gap: var(--space-2);
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
		border: 1px solid var(--accent-border);
		background: var(--accent-surface);
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
