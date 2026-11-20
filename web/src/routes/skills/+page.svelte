<script lang="ts">
	import { onMount } from 'svelte';
	import {
		analyzeSkills,
		applySkillProposal,
		changeSkillStatus,
		createSkill,
		deleteSkill,
		getSkill,
		getWorkingInstructions,
		listSkillProposals,
		listSkills,
		proposeSkillImprovements,
		putWorkingInstructions,
		rateSkill,
		rejectSkillProposal,
		rollbackSkill,
		updateSkill,
		type Skill,
		type SkillFinding,
		type SkillProposal,
		type SkillStatus
	} from '$lib/api';
	import { errorMessage, formatDate } from '$lib/format';
	import { liveRevision } from '$lib/live';
	import { Archive, Check, ChevronDown, GraduationCap, Pencil, Play, Plus, Trash2, X } from 'lucide-svelte';

	type Tab = 'active' | 'draft' | 'disabled' | 'archived';

	const tabLabels: Record<Tab, string> = {
		active: 'Активные',
		draft: 'Черновики',
		disabled: 'Отключённые',
		archived: 'В архиве'
	};

	const statusLabels: Record<SkillStatus, string> = {
		draft: 'черновик',
		active: 'активен',
		disabled: 'отключён',
		archived: 'в архиве'
	};

	let tab: Tab = 'active';
	let skills: Skill[] = [];
	let proposals: SkillProposal[] = [];
	let findings: SkillFinding[] = [];
	let loading = true;
	let error = '';
	let notice = '';
	let busyId: number | null = null;
	let expandedId: number | null = null;
	let detail: Skill | null = null;
	let detailLoading = false;
	let search = '';

	// Create form.
	let createOpen = false;
	let formName = '';
	let formPurpose = '';
	let formWhen = '';
	let formInstructions = '';
	let formLimitations = '';
	let formExamples = '';
	let formTags = '';

	// Edit form (inside the expanded card).
	let editOpen = false;
	let editName = '';
	let editPurpose = '';
	let editWhen = '';
	let editInstructions = '';
	let editLimitations = '';
	let editExamples = '';
	let editTags = '';
	let editNote = '';

	// Feedback form.
	let rating: 'good' | 'bad' = 'good';
	let ratingComment = '';

	// Working instructions.
	let working = '';
	let workingNotice = '';

	async function load() {
		loading = true;
		try {
			const [list, allProposals] = await Promise.all([
				listSkills({ status: tab, q: search || undefined, limit: 50 }),
				listSkillProposals({ status: 'proposed' })
			]);
			skills = list.data;
			proposals = allProposals.data;
			error = '';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			loading = false;
		}
	}

	async function loadWorking() {
		try {
			const result = await getWorkingInstructions();
			working = result.instructions ?? '';
		} catch {
			working = '';
		}
	}

	function selectTab(next: Tab) {
		if (tab === next) return;
		tab = next;
		expandedId = null;
		detail = null;
		load();
	}

	async function toggle(id: number) {
		if (expandedId === id) {
			expandedId = null;
			detail = null;
			editOpen = false;
			return;
		}
		expandedId = id;
		editOpen = false;
		detailLoading = true;
		detail = null;
		try {
			detail = await getSkill(id);
			if (detail) {
				editName = detail.name;
				editPurpose = detail.purpose;
				editWhen = detail.when_to_use;
				editInstructions = detail.instructions;
				editLimitations = detail.limitations;
				editExamples = detail.examples;
				editTags = (detail.tags ?? []).join(', ');
			}
			error = '';
		} catch (err) {
			error = errorMessage(err);
			expandedId = null;
		} finally {
			detailLoading = false;
		}
	}

	async function create() {
		const tags = formTags
			.split(',')
			.map((tag) => tag.trim())
			.filter(Boolean);
		try {
			await createSkill({
				name: formName.trim(),
				purpose: formPurpose.trim(),
				when_to_use: formWhen.trim(),
				instructions: formInstructions.trim(),
				limitations: formLimitations.trim(),
				examples: formExamples.trim(),
				tags
			});
			notice = 'Навык создан черновиком. Активируйте его, когда проверите текст.';
			formName = formPurpose = formWhen = formInstructions = formLimitations = formExamples = formTags = '';
			createOpen = false;
			tab = 'draft';
			await load();
		} catch (err) {
			error = errorMessage(err);
		}
	}

	async function setStatus(item: Skill, status: SkillStatus, message: string) {
		busyId = item.id;
		try {
			const updated = await changeSkillStatus(item.id, status);
			notice = message;
			if (detail && detail.id === item.id) detail = updated;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function remove(item: Skill) {
		if (!confirm(`Удалить навык «${item.name}» вместе с историей версий? Это действие нельзя отменить.`)) return;
		busyId = item.id;
		try {
			await deleteSkill(item.id);
			if (expandedId === item.id) {
				expandedId = null;
				detail = null;
			}
			notice = 'Навык удалён.';
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function saveEdit() {
		if (!detail) return;
		const tags = editTags
			.split(',')
			.map((tag) => tag.trim())
			.filter(Boolean);
		busyId = detail.id;
		try {
			const updated = await updateSkill(
				detail.id,
				{
					name: editName.trim(),
					purpose: editPurpose.trim(),
					when_to_use: editWhen.trim(),
					instructions: editInstructions.trim(),
					limitations: editLimitations.trim(),
					examples: editExamples.trim(),
					tags,
					change_note: editNote.trim()
				},
				detail.version
			);
			detail = updated;
			editOpen = false;
			notice = `Сохранено как версия ${updated.current_version}. Предыдущая версия осталась в истории.`;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function doRollback(versionNo: number) {
		if (!detail) return;
		if (!confirm(`Вернуть навык к версии ${versionNo}? Текущая версия сохранится в истории.`)) return;
		busyId = detail.id;
		try {
			detail = await rollbackSkill(detail.id, versionNo);
			notice = `Возврат выполнен: текущая версия ${detail.current_version}.`;
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function submitRating() {
		if (!detail) return;
		busyId = detail.id;
		try {
			await rateSkill(detail.id, rating, ratingComment.trim());
			ratingComment = '';
			detail = await getSkill(detail.id);
			notice = 'Оценка записана. Она учтётся при будущем анализе, но инструкцию сама не меняет.';
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function runAnalysis(skillId?: number) {
		busyId = skillId ?? -1;
		try {
			const result = await analyzeSkills(skillId);
			findings = result.findings;
			notice =
				result.findings.length > 0
					? `Найдено выводов: ${result.findings.length}. Подготовьте предложения — они не применятся без вашего решения.`
					: 'Выводов нет: по доступным данным ошибок и лишних шагов не видно.';
			error = '';
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function propose(skillId?: number) {
		busyId = skillId ?? -1;
		try {
			const result = await proposeSkillImprovements(skillId);
			notice =
				result.proposals.length > 0
					? `Подготовлено предложений: ${result.proposals.length}. Каждое ждёт вашего решения ниже и в карточке навыка.`
					: 'Предложений нет: анализ не нашёл, что менять.';
			error = '';
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function decideProposal(proposal: SkillProposal, apply: boolean) {
		busyId = proposal.id;
		try {
			if (apply) {
				await applySkillProposal(proposal.id);
				notice = 'Предложение применено. Изменение стало новой версией и доступно для отката.';
			} else {
				await rejectSkillProposal(proposal.id);
				notice = 'Предложение отклонено. Навык не изменён.';
			}
			if (detail) detail = await getSkill(detail.id);
			await load();
		} catch (err) {
			error = errorMessage(err);
		} finally {
			busyId = null;
		}
	}

	async function saveWorking() {
		try {
			const result = await putWorkingInstructions(working.trim());
			working = result.instructions;
			workingNotice = 'Рабочие инструкции сохранены и действуют со следующего обращения.';
		} catch (err) {
			error = errorMessage(err);
		}
	}

	async function refresh() {
		await load();
		if (expandedId !== null) {
			try {
				detail = await getSkill(expandedId);
			} catch {
				expandedId = null;
				detail = null;
			}
		}
	}

	onMount(() => {
		load();
		loadWorking();
		let timer: ReturnType<typeof setTimeout> | undefined;
		const unsubscribe = liveRevision.subscribe((revision) => {
			if (revision === 0) return;
			clearTimeout(timer);
			timer = setTimeout(refresh, 250);
		});
		return () => {
			unsubscribe();
			clearTimeout(timer);
		};
	});
</script>

<svelte:head>
	<title>Навыки — LifeOS</title>
</svelte:head>

<header class="page-header">
	<div>
		<h1>Навыки</h1>
		<p class="subtitle">
			Библиотека проверенных способов решения задач: когда применять, какие шаги выполнять и как
			проверять результат. Ассистент создаёт навыки черновиками и предлагает улучшения — активная
			инструкция меняется только после вашего решения.
		</p>
	</div>
	<button class="primary" on:click={() => (createOpen = !createOpen)}>
		<Plus size={14} aria-hidden="true" /> Новый навык
	</button>
</header>

{#if createOpen}
	<form
		class="create-form"
		on:submit|preventDefault={create}
		aria-label="Создать навык"
	>
		<label>
			Название
			<input bind:value={formName} maxlength="80" required placeholder="Например: Встреча после поездки" />
		</label>
		<label>
			Назначение
			<textarea bind:value={formPurpose} rows="2" maxlength="1000" required></textarea>
		</label>
		<label>
			Когда применять
			<textarea bind:value={formWhen} rows="2" maxlength="1000" required></textarea>
		</label>
		<label>
			Инструкции (шаги)
			<textarea bind:value={formInstructions} rows="5" maxlength="20000" required></textarea>
		</label>
		<label>
			Ограничения
			<textarea bind:value={formLimitations} rows="2" maxlength="4000"></textarea>
		</label>
		<label>
			Примеры
			<textarea bind:value={formExamples} rows="2" maxlength="8000"></textarea>
		</label>
		<label>
			Теги (через запятую)
			<input bind:value={formTags} placeholder="поездка, итоги" />
		</label>
		<div class="form-actions">
			<button type="button" class="ghost" on:click={() => (createOpen = false)}>Отмена</button>
			<button type="submit" class="primary">Создать черновик</button>
		</div>
		<p class="hint">Навык не начнёт применяться, пока вы не активируете его в карточке.</p>
	</form>
{/if}

<section class="working" aria-label="Рабочие инструкции">
	<h2>Рабочие инструкции ассистента</h2>
	<p class="hint">
		Общий блок, который ассистент учитывает в каждом обращении. Улучшения сюда попадают только
		через предложения ниже — как и изменения навыков.
	</p>
	<textarea bind:value={working} rows="3" maxlength="20000" aria-label="Рабочие инструкции"></textarea>
	<div class="form-actions">
		<button class="ghost small" on:click={saveWorking}>Сохранить</button>
		{#if workingNotice}<span class="hint" role="status">{workingNotice}</span>{/if}
	</div>
</section>

<div class="toolbar">
	<div class="tabs" role="tablist" aria-label="Статусы навыков">
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
	<div class="search-row">
		<input
			class="search"
			type="search"
			placeholder="Поиск по навыкам"
			aria-label="Поиск по навыкам"
			bind:value={search}
			on:input={load}
		/>
		<button class="ghost small" disabled={busyId === -1} on:click={() => runAnalysis()}>Анализ работы</button>
		<button class="ghost small" disabled={busyId === -1} on:click={() => propose()}>Подготовить предложения</button>
	</div>
</div>

{#if findings.length > 0}
	<section class="findings" aria-label="Выводы анализа">
		<h2>Выводы анализа</h2>
		<ul>
			{#each findings as finding (finding.skill_name + finding.problem)}
				<li>
					<p><strong>{finding.skill_name}</strong> — {finding.problem}</p>
					{#if finding.evidence.length > 0}
						<ul class="evidence">
							{#each finding.evidence as item}
								<li>{item}</li>
							{/each}
						</ul>
					{/if}
					<p class="hint">Ожидаемый эффект: {finding.expected_effect}</p>
					<p class="hint">Проверка: {finding.test_result}</p>
				</li>
			{/each}
		</ul>
	</section>
{/if}

{#if proposals.length > 0}
	<section class="proposals" aria-label="Предложения по улучшению">
		<h2>Предложения на решение</h2>
		<ul>
			{#each proposals as proposal (proposal.id)}
				<li class="proposal">
					<p>
						<strong>{proposal.kind === 'working_instructions' ? 'Рабочие инструкции' : `Навык №${proposal.skill_id ?? ''}`}</strong>
						— {proposal.problem}
					</p>
					{#if proposal.diff && proposal.diff.length > 0}
						<ul class="evidence">
							{#each proposal.diff as line}
								<li>{line}</li>
							{/each}
						</ul>
					{/if}
					<p class="hint">Ожидаемый эффект: {proposal.expected_effect}</p>
					<p class="hint">Проверка до применения: {proposal.test_result}</p>
					<div class="actions">
						<button class="primary small" disabled={busyId === proposal.id} on:click={() => decideProposal(proposal, true)}>
							<Check size={13} aria-hidden="true" /> Применить
						</button>
						<button class="ghost small" disabled={busyId === proposal.id} on:click={() => decideProposal(proposal, false)}>
							<X size={13} aria-hidden="true" /> Отклонить
						</button>
					</div>
				</li>
			{/each}
		</ul>
	</section>
{/if}

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

{#if loading}
	<p class="empty" role="status">Загрузка…</p>
{:else if skills.length === 0}
	<div class="empty">
		<GraduationCap size={28} strokeWidth={1.5} aria-hidden="true" />
		<p>
			{tab === 'draft'
				? 'Черновиков нет. Попросите ассистента создать навык или создайте его вручную.'
				: tab === 'active'
					? 'Активных навыков нет. Активируйте черновик — он начнёт подключаться к подходящим поручениям.'
					: tab === 'disabled'
						? 'Отключённых навыков нет.'
						: 'Архив пуст.'}
		</p>
	</div>
{:else}
	<ul class="skill-list">
		{#each skills as item (item.id)}
			<li class="skill-card" class:expanded={expandedId === item.id}>
				<button class="card-head" on:click={() => toggle(item.id)} aria-expanded={expandedId === item.id}>
					<div class="title-row">
						<span class="title">{item.name}</span>
						<span class="version">v{item.current_version}</span>
					</div>
					<p class="purpose">{item.purpose}</p>
					<p class="meta">
						<span class={`status-pill ${item.status}`}>{statusLabels[item.status]}</span>
						<span class="meta-item">применений: {item.use_count}</span>
						<span class="meta-item">оценки: {item.good_count} хорошо / {item.bad_count} плохо</span>
						{#if item.tags.length > 0}
							<span class="meta-item">{item.tags.join(', ')}</span>
						{/if}
					</p>
				</button>

				{#if expandedId === item.id}
					<div class="card-detail">
						{#if detailLoading}
							<p class="empty" role="status">Загрузка…</p>
						{:else if detail}
							<div class="facts">
								<div class="fact">
									<span class="fact-label">Назначение</span>
									<span class="fact-value">{detail.purpose}</span>
								</div>
								<div class="fact">
									<span class="fact-label">Когда применять</span>
									<span class="fact-value">{detail.when_to_use}</span>
								</div>
							</div>
							<section aria-label="Инструкции">
								<h2>Инструкции</h2>
								<pre class="instructions">{detail.instructions}</pre>
							</section>
							{#if detail.limitations}
								<section aria-label="Ограничения">
									<h2>Ограничения</h2>
									<pre class="instructions">{detail.limitations}</pre>
								</section>
							{/if}
							{#if detail.examples}
								<section aria-label="Примеры">
									<h2>Примеры</h2>
									<pre class="instructions">{detail.examples}</pre>
								</section>
							{/if}
							<p class="hint">
								Источник: {detail.created_by_assistant ? 'ассистент' : 'пользователь'} · создан
								{formatDate(detail.created_at)} · последнее применение:
								{detail.last_used_at ? formatDate(detail.last_used_at) : 'никогда'}
							</p>

							<div class="actions">
								{#if detail.status === 'draft' || detail.status === 'disabled'}
									<button class="primary small" disabled={busyId === detail.id} on:click={() => setStatus(detail!, 'active', 'Навык активирован и начнёт подключаться к подходящим поручениям.')}>
										<Play size={13} aria-hidden="true" /> Активировать
									</button>
								{/if}
								{#if detail.status === 'active'}
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => setStatus(detail!, 'disabled', 'Навык отключён. Он останется в библиотеке, но перестанет применяться.')}>
										<X size={13} aria-hidden="true" /> Отключить
									</button>
								{/if}
								{#if detail.status !== 'archived'}
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => setStatus(detail!, 'archived', 'Навык отправлен в архив. История версий сохранена.')}>
										<Archive size={13} aria-hidden="true" /> В архив
									</button>
								{/if}
								{#if detail.status === 'archived'}
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => setStatus(detail!, 'draft', 'Навык восстановлен как черновик для правки.')}>
										Восстановить как черновик
									</button>
								{/if}
								<button class="ghost small" on:click={() => (editOpen = !editOpen)}>
									<Pencil size={13} aria-hidden="true" /> {editOpen ? 'Скрыть правку' : 'Править'}
								</button>
								<button class="ghost small danger" disabled={busyId === detail.id} on:click={() => remove(detail!)}>
									<Trash2 size={13} aria-hidden="true" /> Удалить
								</button>
							</div>
							{#if detail.status === 'active' || detail.status === 'disabled'}
								<p class="hint">Удалить можно только черновик или архивный навык: сначала отправьте навык в архив.</p>
							{/if}

							{#if editOpen}
								<form class="edit-form" on:submit|preventDefault={saveEdit} aria-label="Править навык">
									<label>
										Название
										<input bind:value={editName} maxlength="80" required />
									</label>
									<label>
										Назначение
										<textarea bind:value={editPurpose} rows="2" maxlength="1000" required></textarea>
									</label>
									<label>
										Когда применять
										<textarea bind:value={editWhen} rows="2" maxlength="1000" required></textarea>
									</label>
									<label>
										Инструкции
										<textarea bind:value={editInstructions} rows="5" maxlength="20000" required></textarea>
									</label>
									<label>
										Ограничения
										<textarea bind:value={editLimitations} rows="2" maxlength="4000"></textarea>
									</label>
									<label>
										Примеры
										<textarea bind:value={editExamples} rows="2" maxlength="8000"></textarea>
									</label>
									<label>
										Теги (через запятую)
										<input bind:value={editTags} />
									</label>
									<label>
										Пояснение к версии
										<input bind:value={editNote} placeholder="Что изменилось и почему" />
									</label>
									<div class="form-actions">
										<button type="submit" class="primary small" disabled={busyId === detail.id}>Сохранить как новую версию</button>
									</div>
									<p class="hint">Сохранение создаёт новую версию: предыдущая останется в истории и её можно вернуть.</p>
								</form>
							{/if}

							<section aria-label="Версии">
								<h2>История версий</h2>
								{#if detail.versions && detail.versions.length > 0}
									<ul class="versions">
										{#each [...detail.versions].reverse() as version (version.version_no)}
											<li>
												<span class="version">v{version.version_no}</span>
												<span>{version.change_note || 'без пояснения'} · {version.created_by} · {formatDate(version.created_at)}</span>
												{#if version.version_no !== detail.current_version && detail.status !== 'archived'}
													<button class="ghost small" disabled={busyId === detail.id} on:click={() => doRollback(version.version_no)}>
														Вернуть к этой версии
													</button>
												{/if}
											</li>
										{/each}
									</ul>
								{:else}
									<p class="hint">Версий пока нет.</p>
								{/if}
							</section>

							<section aria-label="Оценка навыка">
								<h2>Оценка навыка</h2>
								<div class="rating-row">
									<label>
										<input type="radio" bind:group={rating} value="good" /> Сработал хорошо
									</label>
									<label>
										<input type="radio" bind:group={rating} value="bad" /> Сработал плохо
									</label>
								</div>
								<input
									class="comment"
									type="text"
									placeholder="Комментарий (необязательно)"
									aria-label="Комментарий к оценке"
									bind:value={ratingComment}
									maxlength="2000"
								/>
								<button class="ghost small" disabled={busyId === detail.id} on:click={submitRating}>Записать оценку</button>
								{#if detail.feedback && detail.feedback.length > 0}
									<ul class="versions">
										{#each detail.feedback as item (item.id)}
											<li>{item.rating === 'good' ? 'Хорошо' : 'Плохо'} — {item.comment || 'без комментария'} · {formatDate(item.created_at)}</li>
										{/each}
									</ul>
								{/if}
							</section>

							<section aria-label="Улучшение навыка">
								<h2>Улучшение</h2>
								<div class="actions">
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => runAnalysis(detail!.id)}>Проанализировать работу</button>
									<button class="ghost small" disabled={busyId === detail.id} on:click={() => propose(detail!.id)}>Подготовить предложения</button>
								</div>
								{#if detail.proposals && detail.proposals.length > 0}
									<ul>
										{#each detail.proposals as proposal (proposal.id)}
											<li class="proposal">
												<p><strong>{proposal.status}</strong> — {proposal.problem}</p>
												{#if proposal.diff && proposal.diff.length > 0}
													<ul class="evidence">
														{#each proposal.diff as line}
															<li>{line}</li>
														{/each}
													</ul>
												{/if}
												<p class="hint">Ожидаемый эффект: {proposal.expected_effect}</p>
												<p class="hint">Проверка: {proposal.test_result}</p>
												{#if proposal.status === 'proposed'}
													<div class="actions">
														<button class="primary small" disabled={busyId === proposal.id} on:click={() => decideProposal(proposal, true)}>
															<Check size={13} aria-hidden="true" /> Применить
														</button>
														<button class="ghost small" disabled={busyId === proposal.id} on:click={() => decideProposal(proposal, false)}>
															<X size={13} aria-hidden="true" /> Отклонить
														</button>
													</div>
												{/if}
											</li>
										{/each}
									</ul>
								{/if}
							</section>
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
		max-width: 70ch;
	}

	.primary.small,
	.ghost.small {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		padding: 0.35rem 0.6rem;
		font-size: var(--text-sm);
	}

	.primary {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
		background: var(--accent);
		color: var(--on-accent);
		border: none;
		border-radius: var(--radius-md);
		padding: 0.6rem 0.95rem;
		font-weight: 600;
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

	.primary:disabled,
	.ghost:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}

	.create-form,
	.edit-form,
	.working {
		background: var(--bg-card);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		padding: var(--space-4);
		margin-bottom: var(--space-4);
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}

	label {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		font-size: var(--text-sm);
		color: var(--fg-muted);
	}

	input,
	textarea {
		width: 100%;
		padding: 0.5rem 0.65rem;
		border: 1px solid var(--border);
		border-radius: var(--radius-md);
		background: var(--bg-raised);
		color: var(--fg-primary);
		font: inherit;
		font-size: var(--text-base);
		resize: vertical;
	}

	input:focus-visible,
	textarea:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: 1px;
	}

	.form-actions {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		flex-wrap: wrap;
	}

	.hint {
		margin: 0;
		color: var(--fg-muted);
		font-size: var(--text-sm);
	}

	.toolbar {
		display: flex;
		align-items: flex-end;
		justify-content: space-between;
		gap: var(--space-3);
		flex-wrap: wrap;
		margin-bottom: var(--space-4);
	}

	.tabs {
		display: flex;
		gap: var(--space-1);
		border-bottom: 1px solid var(--border-soft);
	}

	.tab {
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

	.search-row {
		display: flex;
		gap: var(--space-2);
		align-items: center;
		flex-wrap: wrap;
	}

	.search {
		min-width: 12rem;
	}

	.findings,
	.proposals {
		background: var(--bg-card);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		padding: var(--space-4);
		margin-bottom: var(--space-4);
	}

	.findings ul,
	.proposals ul {
		margin: 0;
		padding-left: var(--space-4);
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}

	.evidence {
		margin: var(--space-1) 0 0;
		padding-left: var(--space-4);
		color: var(--fg-muted);
		font-size: var(--text-sm);
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

	.skill-list {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
	}

	.skill-card {
		background: var(--bg-card);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		overflow: hidden;
	}

	.card-head {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		width: 100%;
		padding: var(--space-4);
		border: none;
		background: transparent;
		color: inherit;
		text-align: left;
		cursor: pointer;
	}

	.title-row {
		display: flex;
		align-items: center;
		gap: var(--space-2);
	}

	.title {
		font-weight: 600;
		font-size: var(--text-lg);
	}

	.version {
		color: var(--fg-muted);
		font-size: var(--text-sm);
	}

	.purpose {
		margin: 0;
		color: var(--fg-muted);
		font-size: var(--text-md);
	}

	.meta {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		flex-wrap: wrap;
		margin: 0;
		color: var(--fg-faint);
		font-size: var(--text-sm);
	}

	.status-pill {
		padding: 0.15rem 0.5rem;
		border-radius: var(--radius-sm);
		background: var(--bg-raised);
		font-size: var(--text-xs);
	}

	.card-detail {
		padding: 0 var(--space-4) var(--space-4);
		border-top: 1px solid var(--border-soft);
	}

	.facts {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		margin-top: var(--space-3);
	}

	.fact {
		display: flex;
		flex-direction: column;
		gap: 0.15rem;
	}

	.fact-label {
		color: var(--fg-faint);
		font-size: var(--text-xs);
		text-transform: uppercase;
		letter-spacing: 0.06em;
	}

	.instructions {
		margin: 0;
		padding: var(--space-3);
		border: 1px solid var(--border-soft);
		border-radius: var(--radius-md);
		background: var(--bg-raised);
		white-space: pre-wrap;
		font-family: ui-monospace, monospace;
		font-size: var(--text-sm);
	}

	.actions {
		display: flex;
		gap: var(--space-2);
		flex-wrap: wrap;
		margin-top: var(--space-3);
	}

	.versions {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
	}

	.versions li {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		flex-wrap: wrap;
		font-size: var(--text-sm);
		color: var(--fg-muted);
	}

	.rating-row {
		display: flex;
		gap: var(--space-3);
		margin-bottom: var(--space-2);
	}

	.rating-row label {
		flex-direction: row;
		align-items: center;
		gap: var(--space-1);
	}

	.comment {
		margin-bottom: var(--space-2);
	}

	.proposal {
		border: 1px solid var(--border-soft);
		border-radius: var(--radius-md);
		padding: var(--space-3);
	}

	@media (max-width: 820px) {
		.page-header,
		.toolbar {
			flex-direction: column;
			align-items: stretch;
		}
	}
</style>
