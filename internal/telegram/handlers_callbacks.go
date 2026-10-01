package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"strconv"
	"strings"

	"deadline_bot/internal/domain"
	"deadline_bot/internal/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *Bot) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery, user *domain.User) error {
	data := cb.Data
	if data == "noop" {
		b.answerCallback(cb.ID, "")
		return nil
	}

	// 1. Lab view & personal completion marks
	if strings.HasPrefix(data, "lab:") {
		return b.handleLabCallbacks(ctx, cb, user, data)
	}

	// 2. Settings toggles
	if strings.HasPrefix(data, "set:toggle:") {
		return b.handleSettingsCallback(ctx, cb, user, strings.TrimPrefix(data, "set:toggle:"))
	}

	// 3. Pagination
	if strings.HasPrefix(data, "p:") {
		return b.handlePaginationCallback(ctx, cb, user, strings.TrimPrefix(data, "p:"))
	}

	// 4. Subjects
	if strings.HasPrefix(data, "sub:v:") {
		subID, _ := strconv.ParseInt(strings.TrimPrefix(data, "sub:v:"), 10, 64)
		b.answerCallback(cb.ID, "")
		return b.showSubjectLabs(ctx, cb.Message.Chat.ID, cb.Message.MessageID, user.ID, subID, 1)
	}

	// 5. FSM callbacks
	if strings.HasPrefix(data, "fsm:") {
		return b.handleFSMCallback(ctx, cb, user, data)
	}

	// 6. Admin callbacks
	if strings.HasPrefix(data, "adm:") {
		return b.handleAdminCallback(ctx, cb, user, data)
	}

	b.answerCallback(cb.ID, "")
	return nil
}

func (b *Bot) handleLabCallbacks(ctx context.Context, cb *tgbotapi.CallbackQuery, user *domain.User, data string) error {
	parts := strings.Split(data, ":")
	if len(parts) < 3 {
		b.answerCallback(cb.ID, "")
		return nil
	}
	action := parts[1]
	labID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		b.answerCallback(cb.ID, "Неверный ID работы")
		return nil
	}

	switch action {
	case "v": // View lab card
		b.answerCallback(cb.ID, "")
		return b.showLabCard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, user.ID, labID, "")

	case "done": // Mark as completed
		if err := b.labService.MarkCompleted(ctx, user.ID, labID); err != nil {
			b.answerCallback(cb.ID, fmt.Sprintf("Ошибка: %s", err.Error()))
			return nil
		}
		b.answerCallback(cb.ID, "✅ Работа отмечена как сданная!")
		return b.showLabCard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, user.ID, labID, "")

	case "undone": // Remove completion mark
		if err := b.labService.UnmarkCompleted(ctx, user.ID, labID); err != nil {
			b.answerCallback(cb.ID, fmt.Sprintf("Ошибка: %s", err.Error()))
			return nil
		}
		b.answerCallback(cb.ID, "↩️ Отметка о сдаче снята. Напоминания возобновлены.")
		return b.showLabCard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, user.ID, labID, "")

	case "file": // Send attachment file
		lab, err := b.labService.GetLab(ctx, labID, user.ID)
		if err != nil || lab == nil || lab.FileID == "" {
			b.answerCallback(cb.ID, "Файл не найден")
			return nil
		}
		b.answerCallback(cb.ID, "Отправляю файл...")
		doc := tgbotapi.NewDocument(cb.Message.Chat.ID, tgbotapi.FileID(lab.FileID))
		caption := fmt.Sprintf("📎 Материалы к работе: №%s %s (%s)", lab.Number, lab.Title, lab.SubjectName)
		doc.Caption = caption
		_, sendErr := b.api.Send(doc)
		return sendErr
	}

	b.answerCallback(cb.ID, "")
	return nil
}

func (b *Bot) handleSettingsCallback(ctx context.Context, cb *tgbotapi.CallbackQuery, user *domain.User, key string) error {
	newSettings, err := b.settingsService.ToggleSetting(ctx, user.ID, key)
	if err != nil {
		b.answerCallback(cb.ID, fmt.Sprintf("Ошибка: %s", err.Error()))
		return nil
	}

	b.answerCallback(cb.ID, "Настройки обновлены")
	markup := SettingsKeyboard(newSettings, b.timeHelper.TimezoneName())

	edit := tgbotapi.NewEditMessageReplyMarkup(cb.Message.Chat.ID, cb.Message.MessageID, markup)
	_, err = b.api.Send(edit)
	return err
}

func (b *Bot) handlePaginationCallback(ctx context.Context, cb *tgbotapi.CallbackQuery, user *domain.User, rest string) error {
	parts := strings.Split(rest, ":")
	if len(parts) < 2 {
		b.answerCallback(cb.ID, "")
		return nil
	}

	listType := parts[0]
	page, _ := strconv.Atoi(parts[1])
	if page < 1 {
		page = 1
	}

	b.answerCallback(cb.ID, "")
	if user.GroupID == nil {
		return nil
	}

	switch listType {
	case "deadlines":
		return b.showUpcomingDeadlines(ctx, cb.Message.Chat.ID, cb.Message.MessageID, user.ID, *user.GroupID, page)
	case "week":
		return b.showWeekDeadlines(ctx, cb.Message.Chat.ID, cb.Message.MessageID, user.ID, *user.GroupID, page)
	case "overdue":
		return b.showOverdueDeadlines(ctx, cb.Message.Chat.ID, cb.Message.MessageID, user.ID, *user.GroupID, page)
	case "completed":
		return b.showCompletedDeadlines(ctx, cb.Message.Chat.ID, cb.Message.MessageID, user.ID, *user.GroupID, page)
	case "subj_labs":
		if len(parts) >= 3 {
			subID, _ := strconv.ParseInt(parts[1], 10, 64)
			p, _ := strconv.Atoi(parts[2])
			return b.showSubjectLabs(ctx, cb.Message.Chat.ID, cb.Message.MessageID, user.ID, subID, p)
		}
	case "adm_labs":
		return b.showAdminLabs(ctx, cb.Message.Chat.ID, cb.Message.MessageID, *user.GroupID, page)
	case "adm_users":
		return b.showAdminUsers(ctx, cb.Message.Chat.ID, cb.Message.MessageID, *user.GroupID, page)
	}

	return nil
}

func (b *Bot) handleFSMCallback(ctx context.Context, cb *tgbotapi.CallbackQuery, user *domain.User, data string) error {
	b.answerCallback(cb.ID, "")

	if data == "fsm:cancel" {
		_ = b.fsm.Clear(ctx, user.ID)
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, "❌ Действие отменено.")
		_, err := b.api.Send(edit)
		return err
	}

	if data == "fsm:continue" {
		state, err := b.fsm.GetState(ctx, user.ID)
		if err != nil || state == nil {
			edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, "❌ Не удалось восстановить состояние. Начните заново.")
			_, err := b.api.Send(edit)
			return err
		}
		return b.resumeFSMDialogue(ctx, cb, user, state)
	}

	state, err := b.fsm.GetState(ctx, user.ID)
	if err != nil || state == nil {
		return nil
	}

	draft, _ := b.fsm.GetLabDraft(state)

	switch data {
	case "fsm:back":
		switch state.Step {
		case "description":
			_ = b.fsm.SetState(ctx, user.ID, "create_lab", "number_title", draft)
			prompt := "<b>Шаг 2 из 7: Введите номер и название работы</b>\nПример: <code>1. Основы синтаксиса Go</code>"
			edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
			edit.ParseMode = tgbotapi.ModeHTML
			markup := FSMControls(false, false)
			edit.ReplyMarkup = &markup
			_, err = b.api.Send(edit)
			return err

		case "deadline", "confirm_date_only", "confirm_past":
			_ = b.fsm.SetState(ctx, user.ID, "create_lab", "description", draft)
			prompt := "<b>Шаг 3 из 7: Описание работы</b>\nВведите описание или нажмите «Пропустить»:"
			edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
			edit.ParseMode = tgbotapi.ModeHTML
			markup := FSMControls(true, true)
			edit.ReplyMarkup = &markup
			_, err = b.api.Send(edit)
			return err

		case "materials":
			_ = b.fsm.SetState(ctx, user.ID, "create_lab", "deadline", draft)
			prompt := fmt.Sprintf("<b>Шаг 4 из 7: Дедлайн</b>\nВведите дату и время сдачи (в %s):", b.timeHelper.TimezoneName())
			edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
			edit.ParseMode = tgbotapi.ModeHTML
			markup := FSMControls(true, false)
			edit.ReplyMarkup = &markup
			_, err = b.api.Send(edit)
			return err

		case "submission_method":
			_ = b.fsm.SetState(ctx, user.ID, "create_lab", "materials", draft)
			prompt := "<b>Шаг 5 из 7: Материалы и задание</b>\nОтправьте ссылку или файл, либо нажмите «Пропустить»:"
			edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
			edit.ParseMode = tgbotapi.ModeHTML
			markup := FSMControls(true, true)
			edit.ReplyMarkup = &markup
			_, err = b.api.Send(edit)
			return err

		case "confirm":
			_ = b.fsm.SetState(ctx, user.ID, "create_lab", "submission_method", draft)
			prompt := "<b>Шаг 6 из 7: Способ сдачи и комментарий</b>\nВведите текст или нажмите «Пропустить»:"
			edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
			edit.ParseMode = tgbotapi.ModeHTML
			markup := FSMControls(true, true)
			edit.ReplyMarkup = &markup
			_, err = b.api.Send(edit)
			return err
		}

	case "fsm:skip":
		switch state.Step {
		case "description":
			draft.Description = ""
			_ = b.fsm.SetState(ctx, user.ID, "create_lab", "deadline", draft)
			prompt := fmt.Sprintf("<b>Шаг 4 из 7: Дедлайн</b>\nВведите дату и время сдачи (в %s):", b.timeHelper.TimezoneName())
			edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
			edit.ParseMode = tgbotapi.ModeHTML
			markup := FSMControls(true, false)
			edit.ReplyMarkup = &markup
			_, err = b.api.Send(edit)
			return err

		case "materials":
			draft.SubmissionURL = ""
			draft.FileID = ""
			draft.FileName = ""
			return b.askLabSubmissionMethod(cb.Message.Chat.ID, user.ID, draft)

		case "submission_method":
			draft.SubmissionMethod = ""
			return b.showLabPreviewAndConfirm(cb.Message.Chat.ID, user.ID, draft)
		}

	case "fsm:date_only:ok", "fsm:past:ok":
		// Past or 23:59 confirmed
		draft.ConfirmedPast = true
		return b.askLabMaterials(cb.Message.Chat.ID, user.ID, draft)

	case "fsm:lab:save":
		if user.GroupID == nil {
			return b.sendError(cb.Message.Chat.ID, "Вы не подключены к группе.")
		}
		in := service.CreateLabInput{
			AdminID:          user.ID,
			GroupID:          *user.GroupID,
			SubjectID:        draft.SubjectID,
			Number:           draft.Number,
			Title:            draft.Title,
			Description:      draft.Description,
			SubmissionURL:    draft.SubmissionURL,
			FileID:           draft.FileID,
			SubmissionMethod: draft.SubmissionMethod,
			TeacherComment:   draft.TeacherComment,
			DeadlineAt:       draft.DeadlineAt,
			AllowPast:        draft.ConfirmedPast,
		}
		lab, err := b.labService.CreateLab(ctx, in)
		if err != nil {
			return b.sendError(cb.Message.Chat.ID, fmt.Sprintf("Ошибка сохранения работы: %s", err.Error()))
		}
		_ = b.fsm.Clear(ctx, user.ID)

		text := fmt.Sprintf(
			"🎉 <b>Лабораторная работа №%s %s успешно создана!</b>\n\n"+
				"Предмет: <b>%s</b>\n"+
				"Дедлайн: <b>%s</b>\n\n"+
				"Студентам отправлено уведомление о новой работе и запланированы напоминания.",
			html.EscapeString(lab.Number), html.EscapeString(lab.Title),
			html.EscapeString(lab.SubjectName), b.timeHelper.FormatInGroupTZ(lab.DeadlineAt),
		)
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
		edit.ParseMode = tgbotapi.ModeHTML
		_, err = b.api.Send(edit)
		return err

	case "fsm:rsch:ok":
		rDraft, err := b.fsm.GetRescheduleDraft(state)
		if err != nil || rDraft == nil {
			return nil
		}
		oldDL, newDL, newVer, err := b.labService.RescheduleLabDeadline(ctx, user.ID, rDraft.LabID, rDraft.NewDeadlineAt, true)
		if err != nil {
			return b.sendError(cb.Message.Chat.ID, fmt.Sprintf("Ошибка переноса: %s", err.Error()))
		}
		_ = b.fsm.Clear(ctx, user.ID)

		text := fmt.Sprintf(
			"✅ <b>Дедлайн успешно перенесен!</b>\n\n"+
				"Старый дедлайн: %s\n"+
				"Новый дедлайн: <b>%s</b>\n"+
				"Версия: %d\n\n"+
				"Студентам отправлены уведомления об изменении.",
			b.timeHelper.FormatInGroupTZ(oldDL), b.timeHelper.FormatInGroupTZ(newDL), newVer,
		)
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
		edit.ParseMode = tgbotapi.ModeHTML
		_, err = b.api.Send(edit)
		return err
	}

	return nil
}

func (b *Bot) handleAdminCallback(ctx context.Context, cb *tgbotapi.CallbackQuery, user *domain.User, data string) error {
	// Access check: must be admin or owner
	if !user.IsAdminOrOwner() {
		b.answerCallback(cb.ID, "⛔ Доступ запрещен")
		return nil
	}

	b.answerCallback(cb.ID, "")

	if data == "adm:menu" {
		return b.showAdminMenu(ctx, cb.Message.Chat.ID, cb.Message.MessageID, user.IsOwner())
	}

	if user.GroupID == nil {
		return b.sendError(cb.Message.Chat.ID, "Группа пользователя не настроена. Отправьте /start и повторите действие.")
	}

	if data == "adm:lab:new" {
		// Step 1: Pick subject
		subs, err := b.subjectService.ListActiveSubjects(ctx, *user.GroupID)
		if err != nil || len(subs) == 0 {
			text := "⚠️ В группе нет активных предметов.\nСначала создайте предмет в разделе «📚 Предметы» панели администратора."
			edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
			btnBack := tgbotapi.NewInlineKeyboardButtonData("◀️ В админ-меню", "adm:menu")
			markup := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnBack))
			edit.ReplyMarkup = &markup
			_, err = b.api.Send(edit)
			return err
		}

		var rows [][]tgbotapi.InlineKeyboardButton
		for _, s := range subs {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(s.Name, fmt.Sprintf("adm:fsm:sub:%d", s.ID)),
			))
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "adm:menu"),
		))

		prompt := "<b>Шаг 1 из 7: Выберите предмет</b> для новой лабораторной работы:"
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
		edit.ParseMode = tgbotapi.ModeHTML
		markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
		edit.ReplyMarkup = &markup
		_, err = b.api.Send(edit)
		return err
	}

	if strings.HasPrefix(data, "adm:fsm:sub:") {
		subID, _ := strconv.ParseInt(strings.TrimPrefix(data, "adm:fsm:sub:"), 10, 64)
		sub, err := b.subjectService.GetSubject(ctx, subID)
		if err != nil || sub == nil {
			return b.sendError(cb.Message.Chat.ID, "Предмет не найден.")
		}

		draft := &LabDraft{
			SubjectID:   sub.ID,
			SubjectName: sub.Name,
		}
		_ = b.fsm.SetState(ctx, user.ID, "create_lab", "number_title", draft)

		prompt := fmt.Sprintf(
			"Выбран предмет: <b>%s</b>\n\n"+
				"<b>Шаг 2 из 7: Введите номер и название работы</b>\n"+
				"Пример: <code>1. Основы синтаксиса Go</code>",
			html.EscapeString(sub.Name),
		)
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
		edit.ParseMode = tgbotapi.ModeHTML
		markup := FSMControls(false, false)
		edit.ReplyMarkup = &markup
		_, err = b.api.Send(edit)
		return err
	}

	if strings.HasPrefix(data, "adm:labs:") {
		page, _ := strconv.Atoi(strings.TrimPrefix(data, "adm:labs:"))
		if page < 1 {
			page = 1
		}
		return b.showAdminLabs(ctx, cb.Message.Chat.ID, cb.Message.MessageID, *user.GroupID, page)
	}

	if strings.HasPrefix(data, "adm:lab:v:") {
		labID, _ := strconv.ParseInt(strings.TrimPrefix(data, "adm:lab:v:"), 10, 64)
		return b.showAdminLabDetails(ctx, cb.Message.Chat.ID, cb.Message.MessageID, user.ID, labID)
	}

	if strings.HasPrefix(data, "adm:lab:dl:") {
		labID, _ := strconv.ParseInt(strings.TrimPrefix(data, "adm:lab:dl:"), 10, 64)
		lab, err := b.labService.GetLab(ctx, labID, user.ID)
		if err != nil || lab == nil {
			return b.sendError(cb.Message.Chat.ID, "Работа не найдена.")
		}

		_ = b.fsm.SetState(ctx, user.ID, "reschedule_lab", "input", &RescheduleDraft{
			LabID:    lab.ID,
			LabTitle: lab.Title,
		})

		prompt := fmt.Sprintf(
			"<b>Перенос дедлайна для работы №%s %s (%s)</b>\n\n"+
				"Текущий дедлайн: <b>%s</b>\n\n"+
				"Введите новую дату и время сдачи (в %s):\n"+
				"Пример: <code>28.10.2026 23:59</code>",
			html.EscapeString(lab.Number), html.EscapeString(lab.Title),
			html.EscapeString(lab.SubjectName), b.timeHelper.FormatInGroupTZ(lab.DeadlineAt),
			b.timeHelper.TimezoneName(),
		)
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
		edit.ParseMode = tgbotapi.ModeHTML
		markup := FSMControls(false, false)
		edit.ReplyMarkup = &markup
		_, err = b.api.Send(edit)
		return err
	}

	if strings.HasPrefix(data, "adm:lab:cncl:") {
		labID, _ := strconv.ParseInt(strings.TrimPrefix(data, "adm:lab:cncl:"), 10, 64)
		lab, err := b.labService.GetLab(ctx, labID, user.ID)
		if err != nil || lab == nil {
			return b.sendError(cb.Message.Chat.ID, "Работа не найдена.")
		}

		prompt := fmt.Sprintf(
			"⚠️ <b>Подтверждение отмены лабораторной работы</b>\n\n"+
				"Вы действительно хотите отменить работу <b>№%s %s (%s)</b>?\n\n"+
				"Все запланированные напоминания будут остановлены, а студентам будет отправлено уведомление об отмене.",
			html.EscapeString(lab.Number), html.EscapeString(lab.Title), html.EscapeString(lab.SubjectName),
		)
		btnYes := tgbotapi.NewInlineKeyboardButtonData("❌ Да, отменить работу", fmt.Sprintf("adm:lab:cncl_ok:%d", lab.ID))
		btnNo := tgbotapi.NewInlineKeyboardButtonData("◀️ Назад", fmt.Sprintf("adm:lab:v:%d", lab.ID))

		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
		edit.ParseMode = tgbotapi.ModeHTML
		markup := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(btnYes),
			tgbotapi.NewInlineKeyboardRow(btnNo),
		)
		edit.ReplyMarkup = &markup
		_, err = b.api.Send(edit)
		return err
	}

	if strings.HasPrefix(data, "adm:lab:cncl_ok:") {
		labID, _ := strconv.ParseInt(strings.TrimPrefix(data, "adm:lab:cncl_ok:"), 10, 64)
		if err := b.labService.CancelLab(ctx, user.ID, labID); err != nil {
			return b.sendError(cb.Message.Chat.ID, fmt.Sprintf("Ошибка отмены: %s", err.Error()))
		}
		text := "✅ Лабораторная работа успешно отменена. Уведомления остановлены."
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
		btnBack := tgbotapi.NewInlineKeyboardButtonData("◀️ К списку работ", "adm:labs:1")
		markup := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnBack))
		edit.ReplyMarkup = &markup
		_, err := b.api.Send(edit)
		return err
	}

	if data == "adm:subs" {
		return b.showAdminSubjects(ctx, cb.Message.Chat.ID, cb.Message.MessageID, *user.GroupID)
	}

	if data == "adm:sub:create" {
		_ = b.fsm.SetState(ctx, user.ID, "create_sub", "name", nil)
		prompt := "Введите <b>название нового предмета</b>:"
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
		edit.ParseMode = tgbotapi.ModeHTML
		markup := FSMControls(false, false)
		edit.ReplyMarkup = &markup
		_, err := b.api.Send(edit)
		return err
	}

	if strings.HasPrefix(data, "adm:sub:ren:") {
		subID, _ := strconv.ParseInt(strings.TrimPrefix(data, "adm:sub:ren:"), 10, 64)
		_ = b.fsm.SetState(ctx, user.ID, "rename_sub", fmt.Sprintf("%d", subID), nil)
		prompt := "Введите <b>новое название предмета</b>:"
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
		edit.ParseMode = tgbotapi.ModeHTML
		markup := FSMControls(false, false)
		edit.ReplyMarkup = &markup
		_, err := b.api.Send(edit)
		return err
	}

	if strings.HasPrefix(data, "adm:sub:arc:") {
		subID, _ := strconv.ParseInt(strings.TrimPrefix(data, "adm:sub:arc:"), 10, 64)
		sub, err := b.subjectService.GetSubject(ctx, subID)
		if err == nil && sub != nil {
			_ = b.subjectService.ArchiveSubject(ctx, user.ID, subID, !sub.IsArchived)
		}
		return b.showAdminSubjects(ctx, cb.Message.Chat.ID, cb.Message.MessageID, *user.GroupID)
	}

	if strings.HasPrefix(data, "adm:users:") {
		page, _ := strconv.Atoi(strings.TrimPrefix(data, "adm:users:"))
		if page < 1 {
			page = 1
		}
		return b.showAdminUsers(ctx, cb.Message.Chat.ID, cb.Message.MessageID, *user.GroupID, page)
	}

	if strings.HasPrefix(data, "adm:u:rev:") {
		targetID, _ := strconv.ParseInt(strings.TrimPrefix(data, "adm:u:rev:"), 10, 64)
		if err := b.userService.RevokeStudentAccess(ctx, user.ID, targetID); err != nil {
			b.answerCallback(cb.ID, fmt.Sprintf("Ошибка: %s", err.Error()))
		} else {
			b.answerCallback(cb.ID, "Доступ пользователя отозван.")
		}
		return b.showAdminUsers(ctx, cb.Message.Chat.ID, cb.Message.MessageID, *user.GroupID, 1)
	}

	if strings.HasPrefix(data, "adm:u:rst:") {
		targetID, _ := strconv.ParseInt(strings.TrimPrefix(data, "adm:u:rst:"), 10, 64)
		if err := b.userService.RestoreStudentAccess(ctx, user.ID, targetID); err != nil {
			b.answerCallback(cb.ID, fmt.Sprintf("Ошибка: %s", err.Error()))
		} else {
			b.answerCallback(cb.ID, "Доступ пользователя восстановлен.")
		}
		return b.showAdminUsers(ctx, cb.Message.Chat.ID, cb.Message.MessageID, *user.GroupID, 1)
	}

	if data == "adm:code" {
		grp, _ := b.userService.GetGroup(ctx, *user.GroupID)
		code := "не задан"
		if grp != nil {
			code = grp.InviteCode
		}
		text := fmt.Sprintf(
			"🔑 <b>Код приглашения в группу</b>\n\n"+
				"Текущий код: <code>%s</code>\n\n"+
				"Студенты вводят этот код после запуска бота, чтобы подключиться к группе.\n"+
				"Замена кода не отключает уже подключенных студентов.",
			html.EscapeString(code),
		)
		btnNew := tgbotapi.NewInlineKeyboardButtonData("🔄 Сгенерировать новый код", "adm:code:new")
		btnBack := tgbotapi.NewInlineKeyboardButtonData("◀️ В админ-меню", "adm:menu")

		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
		edit.ParseMode = tgbotapi.ModeHTML
		markup := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(btnNew),
			tgbotapi.NewInlineKeyboardRow(btnBack),
		)
		edit.ReplyMarkup = &markup
		_, err := b.api.Send(edit)
		return err
	}

	if data == "adm:code:new" {
		randomBytes := make([]byte, 4)
		_, _ = rand.Read(randomBytes)
		newCode := fmt.Sprintf("GRP-%s", strings.ToUpper(hex.EncodeToString(randomBytes)))

		if err := b.userService.UpdateInviteCode(ctx, user.ID, *user.GroupID, newCode); err != nil {
			return b.sendError(cb.Message.Chat.ID, fmt.Sprintf("Ошибка обновления кода: %s", err.Error()))
		}

		text := fmt.Sprintf(
			"✅ <b>Новый код приглашения установлен!</b>\n\n"+
				"Код: <code>%s</code>\n\n"+
				"Уже подключенные студенты сохраняют доступ.",
			newCode,
		)
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
		edit.ParseMode = tgbotapi.ModeHTML
		btnBack := tgbotapi.NewInlineKeyboardButtonData("◀️ В админ-меню", "adm:menu")
		markup := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnBack))
		edit.ReplyMarkup = &markup
		_, err := b.api.Send(edit)
		return err
	}

	// Owner actions
	if data == "adm:admins" {
		if !user.IsOwner() {
			b.answerCallback(cb.ID, "Только владелец может управлять администраторами.")
			return nil
		}
		return b.showAdminAdmins(ctx, cb.Message.Chat.ID, cb.Message.MessageID, *user.GroupID)
	}

	if strings.HasPrefix(data, "adm:adm:prm:") {
		if !user.IsOwner() {
			return nil
		}
		targetID, _ := strconv.ParseInt(strings.TrimPrefix(data, "adm:adm:prm:"), 10, 64)
		if err := b.userService.PromoteToAdmin(ctx, user.ID, targetID); err != nil {
			b.answerCallback(cb.ID, fmt.Sprintf("Ошибка: %s", err.Error()))
		} else {
			b.answerCallback(cb.ID, "Студент назначен администратором!")
		}
		return b.showAdminAdmins(ctx, cb.Message.Chat.ID, cb.Message.MessageID, *user.GroupID)
	}

	if strings.HasPrefix(data, "adm:adm:dem:") {
		if !user.IsOwner() {
			return nil
		}
		targetID, _ := strconv.ParseInt(strings.TrimPrefix(data, "adm:adm:dem:"), 10, 64)
		if err := b.userService.DemoteToStudent(ctx, user.ID, targetID); err != nil {
			b.answerCallback(cb.ID, fmt.Sprintf("Ошибка: %s", err.Error()))
		} else {
			b.answerCallback(cb.ID, "Администратор разжалован в студенты.")
		}
		return b.showAdminAdmins(ctx, cb.Message.Chat.ID, cb.Message.MessageID, *user.GroupID)
	}

	if data == "adm:grp:setup" {
		if !user.IsOwner() {
			return nil
		}
		grp, _ := b.userService.GetGroup(ctx, *user.GroupID)
		name := ""
		if grp != nil {
			name = grp.Name
		}
		_ = b.fsm.SetState(ctx, user.ID, "group_setup", "name", &GroupSetupDraft{
			GroupID: *user.GroupID,
		})

		prompt := fmt.Sprintf(
			"⚙️ <b>Настройка группы</b>\n\n"+
				"Текущее название: <b>%s</b>\n\n"+
				"Введите <b>новое название учебной группы</b>:",
			html.EscapeString(name),
		)
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, prompt)
		edit.ParseMode = tgbotapi.ModeHTML
		markup := FSMControls(false, false)
		edit.ReplyMarkup = &markup
		_, err := b.api.Send(edit)
		return err
	}

	return nil
}

func (b *Bot) answerCallback(callbackID, text string) {
	resp := tgbotapi.NewCallback(callbackID, text)
	_, _ = b.api.Request(resp)
}
