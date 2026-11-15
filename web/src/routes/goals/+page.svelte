<script lang="ts">
	import { onMount } from 'svelte';
	import {
		approveGoal,
		applyGoalProposal,
		createGoal,
		createGoalMilestone,
		deleteGoal,
		deleteGoalMilestone,
		listGoals,
		rejectGoalProposal,
		updateGoalMilestone,
		type Goal,
		type GoalStatus
	} from '$lib/api';
	import { errorMessage, formatDate } from '$lib/format';
	import { Target, Check, Plus, Trash2, X, Flag, CircleAlert, ChevronDown, RotateCcw, SkipForward, Play } from 'lucide-svelte';

	type Tab = 'proposed' | 'active' | 'paused' | 'finished';

	const tabLabels: Record<Tab, string> = {
		proposed: 'На утверждении',
		active: 'В работе',
		paused: 'На паузе',
		finished: 'Завершённые'
	};

	const statusLabels: Record<GoalStatus, string> = {
		proposed: 'предложено',
		active: 'в работе',
		paused: 'на паузе',
		achieved: 'достигнуто',
		abandoned: 'отказались'
	};

	const milestoneLabels: Record<string, string> = {
		pending: 'не начат',
		in_progress: 'в работе',
		done: 'выполнен',
		skipped: 'пропущен'
	};

	const kindLabels: Record<string, string> = {
		goal_created: 'цель поставлена',
		approved: 'утверждена',
		milestone: 'этап',
		task_done: 'задача',
		note: 'заметка',
		status: 'статус',
		proposal: 'план'
	};

	const linkLabels: Record<string, string> = {
		conversation: 'диалог',
		task: 'задача',
		event: 'событие',
		document: 'документ',
		memory: 'память',
		inbox: 'входящее'
	};

	let tab: Tab = 'active';
	let goals: Goal[] = [];
	let loading = true;
	let error = '';
	let notice = '';
	let busyId: number | null = null;

	let expandedId: number | null = null;
	let detail: Goal | null = null;
	let detailLoading = false;

	let showCreate = false;
	let newTitle = '';
	let newDescription = '';
	let newTargetDate = '';
	let newMilestones = '';
	let creating = false;

	let newMilestoneTitle = '';

	async function load() {
		loading = true;
		try {
			const statuses: GoalStatus[] | undefined =
				tab === 'finished' ? ['achieved', 'abandoned'] : [tab as GoalStatus];
			const results = await Promise.all(
				(statuses ?? []).map((status) => listGoals({ status, limit: 50 }))
			);
			goals = results.flatMap((result) => result.data);
			goals.sort((a, b) => (a.updated_at < b.updated_at ? 1 : -1));
			error = '';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			loading = false;
		}
	}

	function selectTab(next: Tab) {
		if (tab === next) return;
		tab = next;
		expandedId = null;
		detail = null;
		notice = '';
		load();
	}

	async function toggle(id: number) {
		if (expandedId === id) {
			expandedId = null;
			detail = null;
			return;
		}
		expandedId = id;
		detailLoading = true;
		detail = null;
		try {
			detail = await import('$lib/api').then((api) => api.getGoal(id));
			error = '';
		} catch (err) {
			error = errorMessage(err);
			expandedId = null;
		} finally {
			detailLoading = false;
		}
	}

	async function create() {
		if (!newTitle.trim()) return;
		creating = true;
		try {
			const milestones = newMilestones
				.split('\n')
				.map((line) => line.trim())
				.filter(Boolean)
				.map((line) => ({ title: line }));
			await createGoal({
				title: newTitle.trim(),
				description: newDescription.trim() || undefined,
				target_date: newTargetDate.trim() || undefined,
				milestones: milestones.length > 0 ? milestones : undefined
			});
			newTitle = '';
			newDescription = '';
			newTargetDate = '';
			newMilestones = '';
			showCreate = false;
			notice = 'Цель создана. Начать работу по ней можно после утверждения.';
			selectTab('proposed');
		} catch (err) {
			error = errorMessage(err);
		} finally {
			creating = false;
		}
	}

	async function approve(goal: Goal) {
		busyId = goal.id;
		try {
			const updated = await approveGoal(goal.id);
			notice = 'Цель утверждена — работа началась.';
			if (detail && detail.id === goal.id) detail = updated;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function setGoalStatus(goal: Goal, status: 'paused' | 'active' | 'achieved' | 'abandoned') {
		busyId = goal.id;
		try {
			const { changeGoalStatus } = await import('$lib/api');
			const updated = await changeGoalStatus(goal.id, status);
			if (detail && detail.id === goal.id) detail = updated;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function remove(goal: Goal) {
		if (!confirm(`Удалить цель «${goal.title}» вместе с планом, связями и журналом?`)) return;
		busyId = goal.id;
		try {
			await deleteGoal(goal.id);
			if (expandedId === goal.id) {
				expandedId = null;
				detail = null;
			}
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function milestoneAction(milestoneId: number, version: number, status: 'in_progress' | 'done' | 'skipped' | 'pending') {
		if (!detail) return;
		busyId = detail.id;
		try {
			const updated = await updateGoalMilestone(detail.id, milestoneId, version, { status });
			detail = updated;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function addMilestone() {
		if (!detail || !newMilestoneTitle.trim()) return;
		busyId = detail.id;
		try {
			const updated = await createGoalMilestone(detail.id, { title: newMilestoneTitle.trim() });
			detail = updated;
			newMilestoneTitle = '';
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function decideProposal(proposalId: number, decision: 'apply' | 'reject') {
		if (!detail) return;
		busyId = detail.id;
		try {
			const updated =
				decision === 'apply'
					? await applyGoalProposal(detail.id, proposalId)
					: await rejectGoalProposal(detail.id, proposalId);
			detail = updated;
			notice = decision === 'apply' ? 'Изменение плана применено.' : 'Предложение отклонено.';
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	function describeChanges(changes?: Record<string, unknown>): string {
		if (!changes) return '';
		const parts: string[] = [];
		if (typeof changes.title === 'string') parts.push(`название: «${changes.title}»`);
		if (typeof changes.description === 'string') parts.push('новое описание');
		if (typeof changes.target_date === 'string') parts.push(`срок: ${changes.target_date}`);
		if (changes.clear_target_date) parts.push('срок убран');
		const add = (changes.add_milestones as { title: string }[] | undefined) ?? [];
		if (add.length > 0) parts.push(`добавить этапов: ${add.length}`);
		const count = (key: string, label: string) => {
			const value = (changes[key] as unknown[] | undefined) ?? [];
			if (value.length > 0) parts.push(`${label}: ${value.length}`);
		};
		count('complete_milestone_ids', 'выполнить этапов');
		count('skip_milestone_ids', 'пропустить этапов');
		count('reopen_milestone_ids', 'вернуть этапов');
		count('remove_milestone_ids', 'убрать этапов');
		return parts.join(', ');
	}

	function signalsFor(goal: Goal): string[] {
		const signals: string[] = [];
		if (goal.signals?.stalled) signals.push(goal.signals.stall_reason ?? 'нет активности');
		if (goal.signals?.overdue) signals.push('срок истёк');
		if (goal.signals?.pending_proposal) signals.push('ждёт решения по плану');
		return signals;
	}

	onMount(load);
</script>

<svelte:head>
	<title>Цели — LifeOS</title>
</svelte:head>

<header class="page-header">
	<div>
		<h1>Цели</h1>
		<p class="subtitle">Долгосрочные намерения: план по этапам, фактический прогресс и решения по изменениям</p>
	</div>
	<button class="primary" on:click={() => (showCreate = !showCreate)}>
		{showCreate ? 'Скрыть форму' : 'Поставить цель'}
	</button>
</header>

<div class="tabs" role="tablist" aria-label="Статусы целей">
	{#each Object.entries(tabLabels) as [key, label] (key)}
		<button
			class="tab"
			class:active={tab === key}
			role="tab"
			aria-selected={tab === key}
			on:click={() => selectTab(key as Tab)}
		>
			{label}
		</button>
	{/each}
</div>

{#if error}
	<div class="banner" role="alert">
		<span>{error}</span>
		<button on:click={() => (error = '')} aria-label="Закрыть сообщение об ошибке">×</button>
	</div>
{/if}

{#if notice}
	<div class="notice" role="status">
		<span>{notice}</span>
		<button on:click={() => (notice = '')} aria-label="Закрыть уведомление">×</button>
	</div>
{/if}

{#if showCreate}
	<form class="create" on:submit|preventDefault={create}>
		<input
			bind:value={newTitle}
			placeholder="Название цели: например «Выучить английский до C1»"
			aria-label="Название цели"
			autocomplete="off"
		/>
		<textarea
			bind:value={newDescription}
			rows="2"
			placeholder="Что означает достижение цели и как это проверить"
			aria-label="Описание цели"
		></textarea>
		<div class="create-row">
			<input type="date" bind:value={newTargetDate} aria-label="Желаемый срок" />
			<button class="primary" type="submit" disabled={creating || !newTitle.trim()}>
				{creating ? 'Сохранение…' : 'Поставить цель'}
			</button>
		</div>
		<textarea
			bind:value={newMilestones}
			rows="3"
			placeholder="Этапы плана — по одному на строку (необязательно)"
			aria-label="Этапы плана"
		></textarea>
	</form>
{/if}

{#if loading}
	<p class="empty" role="status">Загрузка…</p>
{:else if goals.length === 0}
	<div class="empty">
		<Target size={28} strokeWidth={1.5} aria-hidden="true" />
		<p>
			{tab === 'proposed'
				? 'Новых целей нет. Поставьте цель сами или попросите ассистента составить план.'
				: tab === 'active'
					? 'Сейчас ни одна цель не в работе.'
					: tab === 'paused'
						? 'Пауза не нужна: активных целей нет.'
						: 'Завершённых целей пока нет.'}
		</p>
	</div>
{:else}
	<ul class="goal-list">
		{#each goals as goal (goal.id)}
			<li class="goal-card" class:expanded={expandedId === goal.id}>
				<button class="goal-head" on:click={() => toggle(goal.id)} aria-expanded={expandedId === goal.id}>
					<div class="goal-title-row">
						<span class="goal-title">{goal.title}</span>
						{#if goal.progress?.percent !== null && goal.progress?.percent !== undefined}
							<span class="percent">{goal.progress.percent}%</span>
						{/if}
					</div>
					<p class="goal-meta">
						<span class={`status-pill ${goal.status}`}>{statusLabels[goal.status]}</span>
						{#if goal.target_date}
							<span class="meta-item">до {formatDate(goal.target_date)}</span>
						{/if}
						{#if goal.progress && goal.progress.milestones_total > 0}
							<span class="meta-item">этапы: {goal.progress.milestones_done}/{goal.progress.milestones_total}</span>
						{/if}
					</p>
					{#if goal.progress?.percent !== null && goal.progress?.percent !== undefined}
						<progress class="bar" max="100" value={goal.progress.percent} aria-label="Прогресс цели"></progress>
					{/if}
					{#each signalsFor(goal) as signal (signal)}
						<span class="signal"><CircleAlert size={13} aria-hidden="true" /> {signal}</span>
					{/each}
				</button>

				{#if expandedId === goal.id}
					<div class="goal-detail">
						{#if detailLoading}
							<p class="empty" role="status">Загрузка…</p>
						{:else if detail}
							{#if detail.description}
								<p class="description">{detail.description}</p>
							{/if}

							<div class="actions">
								{#if detail.status === 'proposed'}
									<button class="primary small" disabled={busyId === detail.id} on:click={() => approve(detail!)}>
										<Check size={13} aria-hidden="true" /> Утвердить и начать
									</button>
								{/if}
								{#if detail.status === 'active'}
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => setGoalStatus(detail!, 'paused')}>
										На паузу
									</button>
								{/if}
								{#if detail.status === 'paused'}
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => setGoalStatus(detail!, 'active')}>
										Возобновить
									</button>
								{/if}
								{#if detail.status === 'active' || detail.status === 'paused'}
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => setGoalStatus(detail!, 'achieved')}>
										<Flag size={13} aria-hidden="true" /> Достигнута
									</button>
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => setGoalStatus(detail!, 'abandoned')}>
										Отказаться
									</button>
								{/if}
								<button class="ghost small danger" disabled={busyId === detail.id} on:click={() => remove(detail!)}>
									<Trash2 size={13} aria-hidden="true" /> Удалить
								</button>
							</div>

							<section aria-label="План этапов">
								<h2>План</h2>
								<ul class="milestones">
									{#each detail.milestones as milestone (milestone.id)}
										<li class="milestone">
											<div class="milestone-main">
												<span class="milestone-title" class:done={milestone.status === 'done'}>{milestone.title}</span>
												{#if milestone.criteria}
													<span class="milestone-criteria">{milestone.criteria}</span>
												{/if}
												{#if milestone.due_date}
													<span class="milestone-criteria">срок {formatDate(milestone.due_date)}</span>
												{/if}
											</div>
											<span class={`milestone-status ${milestone.status}`}>{milestoneLabels[milestone.status]}</span>
											<div class="milestone-actions">
												{#if milestone.status === 'pending'}
													<button class="icon" title="Начать этап" aria-label={`Начать этап «${milestone.title}»`} disabled={busyId === detail.id} on:click={() => milestoneAction(milestone.id, milestone.version, 'in_progress')}>
														<Play size={13} aria-hidden="true" />
													</button>
													<button class="icon" title="Этап выполнен" aria-label={`Отметить «${milestone.title}» выполненным`} disabled={busyId === detail.id} on:click={() => milestoneAction(milestone.id, milestone.version, 'done')}>
														<Check size={13} aria-hidden="true" />
													</button>
													<button class="icon" title="Пропустить этап" aria-label={`Пропустить «${milestone.title}»`} disabled={busyId === detail.id} on:click={() => milestoneAction(milestone.id, milestone.version, 'skipped')}>
														<SkipForward size={13} aria-hidden="true" />
													</button>
												{:else if milestone.status === 'in_progress'}
													<button class="icon" title="Этап выполнен" aria-label={`Отметить «${milestone.title}» выполненным`} disabled={busyId === detail.id} on:click={() => milestoneAction(milestone.id, milestone.version, 'done')}>
														<Check size={13} aria-hidden="true" />
													</button>
													<button class="icon" title="Пропустить этап" aria-label={`Пропустить «${milestone.title}»`} disabled={busyId === detail.id} on:click={() => milestoneAction(milestone.id, milestone.version, 'skipped')}>
														<SkipForward size={13} aria-hidden="true" />
													</button>
												{:else}
													<button class="icon" title="Вернуть в работу" aria-label={`Вернуть «${milestone.title}» в работу`} disabled={busyId === detail.id} on:click={() => milestoneAction(milestone.id, milestone.version, 'pending')}>
														<RotateCcw size={13} aria-hidden="true" />
													</button>
												{/if}
											</div>
										</li>
									{/each}
									{#if detail.milestones.length === 0}
										<li class="milestone-empty">План пока пуст. Попросите ассистента разбить цель на этапы.</li>
									{/if}
								</ul>
								{#if detail.status !== 'proposed' && detail.status !== 'active'}
									<div class="add-milestone">
										<input
											bind:value={newMilestoneTitle}
											placeholder="Добавить этап в план"
											aria-label="Название нового этапа"
											autocomplete="off"
										/>
										<button class="ghost small" on:click={addMilestone} disabled={busyId === detail.id || !newMilestoneTitle.trim()}>
											<Plus size={13} aria-hidden="true" /> Добавить
										</button>
									</div>
								{:else if detail.status === 'active'}
									<p class="hint">Утверждённый план меняется через предложение: попросите ассистента, и оно появится здесь.</p>
								{/if}
							</section>

							{#if detail.proposals.filter((p) => p.status === 'pending').length > 0}
								<section aria-label="Предложения по плану">
									<h2>Предложения по плану</h2>
									<ul class="proposals">
										{#each detail.proposals.filter((p) => p.status === 'pending') as proposal (proposal.id)}
											<li class="proposal">
												<p class="proposal-summary">{proposal.summary}</p>
												{#if describeChanges(proposal.changes)}
													<p class="proposal-changes">{describeChanges(proposal.changes)}</p>
												{/if}
												<div class="proposal-actions">
													<button class="primary small" disabled={busyId === detail.id} on:click={() => decideProposal(proposal.id, 'apply')}>
														Применить
													</button>
													<button class="ghost small" disabled={busyId === detail.id} on:click={() => decideProposal(proposal.id, 'reject')}>
														Отклонить
													</button>
												</div>
											</li>
										{/each}
									</ul>
								</section>
							{/if}

							{#if detail.links.length > 0}
								<section aria-label="Связи цели">
									<h2>Связи</h2>
									<ul class="links">
										{#each detail.links as link (link.id)}
											<li>
												<span class="link-kind">{linkLabels[link.target_type] ?? link.target_type} #{link.target_id}</span>
												<span class="link-relation">{link.relation}</span>
											</li>
										{/each}
									</ul>
								</section>
							{/if}

							{#if detail.progress_log.length > 0}
								<section aria-label="Журнал прогресса">
									<h2>Прогресс</h2>
									<ul class="journal">
										{#each detail.progress_log.slice(0, 12) as event (event.id)}
											<li>
												<span class="journal-kind">{kindLabels[event.kind] ?? event.kind}</span>
												<span class="journal-summary">{event.summary}</span>
												<span class="journal-date">{formatDate(event.recorded_at)}</span>
											</li>
										{/each}
									</ul>
								</section>
							{/if}
						{/if}
					</div>
				{/if}
			</li>
		{/each}
	</ul>
{/if}

<style>
	.page-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-4);
		margin-bottom: var(--space-5);
	}

	h1 {
		margin: 0;
		font-size: var(--text-2xl);
		letter-spacing: -0.02em;
	}

	h2 {
		margin: var(--space-3) 0 var(--space-2);
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--fg-faint);
		font-weight: 600;
	}

	.subtitle {
		margin: var(--space-1) 0 0;
		color: var(--fg-muted);
		font-size: var(--text-md);
	}

	.primary {
		background: var(--accent);
		color: var(--on-accent);
		border: none;
		border-radius: var(--radius-md);
		padding: 0.6rem 0.95rem;
		font-weight: 600;
	}

	.primary.small,
	.ghost.small {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		padding: 0.35rem 0.6rem;
		font-size: var(--text-sm);
	}

	.primary:disabled {
		opacity: 0.55;
		cursor: default;
	}

	.ghost {
		background: transparent;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		color: var(--fg-body);
		padding: 0.45rem 0.8rem;
		font-size: var(--text-base);
	}

	.ghost:hover {
		background: var(--bg-raised);
	}

	.ghost.danger {
		color: var(--danger-text);
		border-color: var(--danger-border);
	}

	.ghost:disabled,
	.icon:disabled,
	.primary:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.icon {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 26px;
		height: 26px;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: transparent;
		color: var(--fg-muted);
	}

	.icon:hover {
		color: var(--fg-primary);
		background: var(--bg-raised);
	}

	.tabs {
		display: flex;
		gap: var(--space-1);
		margin-bottom: var(--space-4);
		border-bottom: 1px solid var(--border-soft);
	}

	.tab {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		padding: 0.55rem 0.7rem;
		border: none;
		border-bottom: 2px solid transparent;
		background: transparent;
		color: var(--fg-muted);
		font-size: var(--text-base);
	}

	.tab:hover {
		color: var(--fg-primary);
	}

	.tab.active {
		color: var(--fg-primary);
		border-bottom-color: var(--accent);
	}

	.banner,
	.notice {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
		padding: 0.6rem 0.8rem;
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		margin-bottom: var(--space-4);
	}

	.banner {
		background: var(--danger-surface);
		border: 1px solid var(--danger-border);
		color: var(--danger-text);
	}

	.notice {
		background: var(--success-surface);
		border: 1px solid rgba(52, 211, 153, 0.3);
		color: var(--text);
	}

	.create {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		padding: var(--space-3);
		margin-bottom: var(--space-4);
		background: var(--bg-card);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
	}

	.create-row {
		display: flex;
		gap: var(--space-2);
		align-items: center;
	}

	.create input,
	.create textarea {
		width: 100%;
		background: var(--bg-primary);
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		color: var(--fg-body);
		padding: 0.5rem 0.7rem;
		font-size: var(--text-base);
	}

	.create input:focus-visible,
	.create textarea:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: 1px;
	}

	.create textarea {
		resize: vertical;
	}

	.create .primary {
		flex-shrink: 0;
	}

	.empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--space-2);
		color: var(--fg-muted);
		padding: var(--space-6) 0;
		text-align: center;
	}

	.goal-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}

	.goal-card {
		background: var(--bg-card);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		overflow: hidden;
	}

	.goal-card.expanded {
		border-color: var(--accent-border);
	}

	.goal-head {
		display: block;
		width: 100%;
		text-align: left;
		background: transparent;
		border: none;
		color: inherit;
		padding: var(--space-3) var(--space-4);
		cursor: pointer;
	}

	.goal-head:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: -2px;
	}

	.goal-title-row {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-3);
	}

	.goal-title {
		font-weight: 600;
		font-size: var(--text-lg);
		color: var(--fg-primary);
	}

	.percent {
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		color: var(--accent-bright);
	}

	.goal-meta {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		margin: var(--space-1) 0 0;
		font-size: var(--text-sm);
		color: var(--fg-muted);
	}

	.status-pill {
		padding: 0.1rem 0.5rem;
		border-radius: 999px;
		font-size: var(--text-2xs);
		border: 1px solid var(--border);
		color: var(--fg-muted);
	}

	.status-pill.active {
		color: var(--success);
		border-color: rgba(52, 211, 153, 0.4);
	}

	.status-pill.proposed {
		color: var(--accent-bright);
		border-color: var(--accent-border);
	}

	.status-pill.paused {
		color: var(--warning);
		border-color: var(--warning-border);
	}

	.status-pill.achieved {
		color: var(--success);
	}

	.status-pill.abandoned {
		color: var(--fg-faint);
	}

	.bar {
		width: 100%;
		height: 6px;
		margin-top: var(--space-2);
		border: none;
		border-radius: 3px;
		background: var(--bg-raised);
	}

	.bar::-webkit-progress-bar {
		background: var(--bg-raised);
		border-radius: 3px;
	}

	.bar::-webkit-progress-value {
		background: var(--accent);
		border-radius: 3px;
	}

	.bar::-moz-progress-bar {
		background: var(--accent);
		border-radius: 3px;
	}

	.signal {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		margin: var(--space-2) var(--space-2) 0 0;
		padding: 0.15rem 0.5rem;
		border-radius: 999px;
		background: var(--warning-surface);
		border: 1px solid var(--warning-border);
		color: var(--warning);
		font-size: var(--text-xs);
	}

	.goal-detail {
		border-top: 1px solid var(--border-soft);
		padding: var(--space-3) var(--space-4) var(--space-4);
	}

	.description {
		color: var(--fg-body);
		font-size: var(--text-base);
		margin: 0 0 var(--space-3);
	}

	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin-bottom: var(--space-2);
	}

	.milestones {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
	}

	.milestone {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-2) 0;
		border-bottom: 1px solid var(--border-soft);
	}

	.milestone:last-child {
		border-bottom: none;
	}

	.milestone-main {
		flex: 1;
		min-width: 0;
	}

	.milestone-title {
		display: block;
		color: var(--fg-primary);
	}

	.milestone-title.done {
		text-decoration: line-through;
		color: var(--fg-faint);
	}

	.milestone-criteria {
		display: block;
		font-size: var(--text-sm);
		color: var(--fg-faint);
	}

	.milestone-status {
		flex-shrink: 0;
		font-size: var(--text-xs);
		color: var(--fg-muted);
	}

	.milestone-status.done {
		color: var(--success);
	}

	.milestone-status.in_progress {
		color: var(--accent-bright);
	}

	.milestone-status.skipped {
		color: var(--fg-faint);
	}

	.milestone-actions {
		display: flex;
		gap: var(--space-1);
		flex-shrink: 0;
	}

	.milestone-empty {
		padding: var(--space-2) 0;
		color: var(--fg-faint);
		font-size: var(--text-sm);
	}

	.add-milestone {
		display: flex;
		gap: var(--space-2);
		margin-top: var(--space-2);
	}

	.add-milestone input {
		flex: 1;
		background: var(--bg-primary);
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		color: var(--fg-body);
		padding: 0.4rem 0.6rem;
		font-size: var(--text-sm);
	}

	.add-milestone input:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: 1px;
	}

	.hint {
		margin: var(--space-2) 0 0;
		font-size: var(--text-sm);
		color: var(--fg-faint);
	}

	.proposals {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}

	.proposal {
		padding: var(--space-3);
		border: 1px solid var(--warning-border);
		border-radius: var(--radius-md);
		background: var(--warning-surface);
	}

	.proposal-summary {
		margin: 0;
		color: var(--fg-primary);
	}

	.proposal-changes {
		margin: var(--space-1) 0 0;
		font-size: var(--text-sm);
		color: var(--fg-muted);
	}

	.proposal-actions {
		display: flex;
		gap: var(--space-2);
		margin-top: var(--space-2);
	}

	.links {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}

	.links li {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		padding: 0.25rem 0.6rem;
		border: 1px solid var(--border);
		border-radius: 999px;
		font-size: var(--text-xs);
	}

	.link-kind {
		color: var(--fg-muted);
	}

	.link-relation {
		color: var(--fg-faint);
	}

	.journal {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
	}

	.journal li {
		display: flex;
		align-items: baseline;
		gap: var(--space-2);
		font-size: var(--text-sm);
	}

	.journal-kind {
		flex-shrink: 0;
		color: var(--fg-faint);
		font-size: var(--text-2xs);
		text-transform: uppercase;
		letter-spacing: 0.05em;
	}

	.journal-summary {
		flex: 1;
		color: var(--fg-body);
	}

	.journal-date {
		flex-shrink: 0;
		color: var(--fg-faint);
		font-size: var(--text-xs);
	}

	@media (max-width: 640px) {
		.page-header {
			flex-direction: column;
		}

		.create-row {
			flex-direction: column;
			align-items: stretch;
		}

		.tabs {
			overflow-x: auto;
		}

		.milestone {
			flex-wrap: wrap;
		}
	}
</style>
