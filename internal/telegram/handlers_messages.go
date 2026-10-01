package telegram

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strings"

	"deadline_bot/internal/domain"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *Bot) handleMessage(ctx context.Context, msg *tgbotapi.Message, user *domain.User) error {
	// If it's a bot command, handle it via command router
	if msg.IsCommand() {
		return b.handleCommand(ctx, msg, user)
	}

	// Check if user is in an active FSM dialogue
	state, err := b.fsm.GetState(ctx, user.ID)
	if err == nil && state != nil && state.State != "" {
		return b.handleFSMMessage(ctx, msg, user, state)
	}

	// Check reply keyboard buttons
	switch msg.Text {
	case "📅 Ближайшие дедлайны":
		return b.cmdDeadlines(ctx, msg, user)
	case "🗓 На этой неделе":
		if user.GroupID == nil || user.Status != domain.UserStatusActive {
			return b.sendInvitePrompt(msg.Chat.ID)
		}
		return b.showWeekDeadlines(ctx, msg.Chat.ID, 0, user.ID, *user.GroupID, 1)
	case "📚 Предметы":
		return b.cmdSubjects(ctx, msg, user)
	case "⚠️ Просроченные":
		if user.GroupID == nil || user.Status != domain.UserStatusActive {
			return b.sendInvitePrompt(msg.Chat.ID)
		}
		return b.showOverdueDeadlines(ctx, msg.Chat.ID, 0, user.ID, *user.GroupID, 1)
	case "✅ Сданные":
		if user.GroupID == nil || user.Status != domain.UserStatusActive {
			return b.sendInvitePrompt(msg.Chat.ID)
		}
		return b.showCompletedDeadlines(ctx, msg.Chat.ID, 0, user.ID, *user.GroupID, 1)
	case "⚙️ Настройки":
		return b.cmdSettings(ctx, msg, user)
	case "⚡ Панель администратора":
		return b.cmdAdmin(ctx, msg, user)
	}

	// Fallback response
	reply := tgbotapi.NewMessage(msg.Chat.ID, "Пожалуйста, используйте кнопки меню или введите /help для справки.")
	reply.ReplyMarkup = MainMenuKeyboard(user.IsAdminOrOwner())
	_, err = b.api.Send(reply)
	return err
}

func (b *Bot) handleFSMMessage(ctx context.Context, msg *tgbotapi.Message, user *domain.User, ds *domain.DialogueState) error {
	switch ds.State {
	case "enter_invite":
		return b.handleFSMInviteCode(ctx, msg, user)
	case "create_lab":
		return b.handleFSMCreateLabMessage(ctx, msg, user, ds)
	case "reschedule_lab":
		return b.handleFSMRescheduleMessage(ctx, msg, user, ds)
	case "create_sub":
		return b.handleFSMCreateSubject(ctx, msg, user)
	case "rename_sub":
		return b.handleFSMRenameSubject(ctx, msg, user, ds)
	case "group_setup":
		return b.handleFSMGroupSetup(ctx, msg, user, ds)
	default:
		_ = b.fsm.Clear(ctx, user.ID)
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Предыдущее действие сброшено. Выберите раздел меню.")
		reply.ReplyMarkup = MainMenuKeyboard(user.IsAdminOrOwner())
		_, err := b.api.Send(reply)
		return err
	}
}

func (b *Bot) handleFSMInviteCode(ctx context.Context, msg *tgbotapi.Message, user *domain.User) error {
	code := strings.TrimSpace(msg.Text)
	grp, err := b.userService.JoinGroup(ctx, user.ID, code)
	if err != nil {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "❌ <b>Неверный код приглашения.</b>\nПожалуйста, проверьте код и попробуйте еще раз:")
		reply.ParseMode = tgbotapi.ModeHTML
		_, err := b.api.Send(reply)
		return err
	}

	_ = b.fsm.Clear(ctx, user.ID)
	// Refresh user info
	u, _ := b.storage.GetUserByID(ctx, user.ID)
	isAdmin := false
	if u != nil {
		isAdmin = u.IsAdminOrOwner()
	}

	text := fmt.Sprintf(
		"🎉 <b>Вы успешно подключились к группе «%s»!</b>\n\n"+
			"Теперь вы будете получать персональные уведомления о дедлайнах.\n"+
			"Используйте меню ниже для навигации:",
		html.EscapeString(grp.Name),
	)
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = tgbotapi.ModeHTML
	reply.ReplyMarkup = MainMenuKeyboard(isAdmin)
	_, err = b.api.Send(reply)
	return err
}

func (b *Bot) handleFSMCreateLabMessage(ctx context.Context, msg *tgbotapi.Message, user *domain.User, ds *domain.DialogueState) error {
	draft, err := b.fsm.GetLabDraft(ds)
	if err != nil {
		_ = b.fsm.Clear(ctx, user.ID)
		return b.sendError(msg.Chat.ID, "Ошибка чтения черновика. Пожалуйста, начните заново.")
	}

	switch ds.Step {
	case "number_title":
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			return b.sendError(msg.Chat.ID, "Номер и название работы не могут быть пустыми. Повторите ввод:")
		}
		num, title := ParseNumberAndTitle(text)
		draft.Number = num
		draft.Title = title

		_ = b.fsm.SetState(ctx, user.ID, "create_lab", "description", draft)

		prompt := fmt.Sprintf(
			"✅ Номер: <b>%s</b>, Название: <b>%s</b>\n\n"+
				"<b>Шаг 3 из 7: Описание работы</b>\n"+
				"Введите краткое описание работы или нажмите «Пропустить»:",
			html.EscapeString(draft.Number), html.EscapeString(draft.Title),
		)
		reply := tgbotapi.NewMessage(msg.Chat.ID, prompt)
		reply.ParseMode = tgbotapi.ModeHTML
		reply.ReplyMarkup = FSMControls(true, true)
		_, err = b.api.Send(reply)
		return err

	case "description":
		draft.Description = strings.TrimSpace(msg.Text)
		_ = b.fsm.SetState(ctx, user.ID, "create_lab", "deadline", draft)

		prompt := fmt.Sprintf(
			"<b>Шаг 4 из 7: Дедлайн (срок сдачи)</b>\n"+
				"Введите дату и время сдачи в часовом поясе <b>%s</b>.\n\n"+
				"Примеры:\n"+
				"• <code>25.10.2026 23:59</code>\n"+
				"• <code>25.10.2026</code> (будет предложено время 23:59)",
			b.timeHelper.TimezoneName(),
		)
		reply := tgbotapi.NewMessage(msg.Chat.ID, prompt)
		reply.ParseMode = tgbotapi.ModeHTML
		reply.ReplyMarkup = FSMControls(true, false)
		_, err = b.api.Send(reply)
		return err

	case "deadline":
		dl, dateOnly, err := b.timeHelper.ParseDeadline(msg.Text)
		if err != nil {
			prompt := fmt.Sprintf("❌ %s\nПожалуйста, введите дату снова:", err.Error())
			reply := tgbotapi.NewMessage(msg.Chat.ID, prompt)
			reply.ReplyMarkup = FSMControls(true, false)
			_, err = b.api.Send(reply)
			return err
		}

		draft.DeadlineAt = dl
		draft.IsDateOnly = dateOnly

		now := b.clock.Now()
		isPast := dl.Before(now)

		if dateOnly {
			// Explicit prompt for 23:59 requirement
			draft.ConfirmedPast = false
			_ = b.fsm.SetState(ctx, user.ID, "create_lab", "confirm_date_only", draft)

			formatted := b.timeHelper.FormatInGroupTZ(dl)
			prompt := fmt.Sprintf(
				"ℹ️ Вы указали только дату. Предлагаемое время сдачи: <b>23:59</b>.\n\n"+
					"Итоговый дедлайн: <b>%s</b>\n\n"+
					"Подтвердить это время?",
				formatted,
			)
			if isPast {
				prompt += "\n\n⚠️ <i>Внимание: эта дата уже в прошлом!</i>"
			}

			btnOk := tgbotapi.NewInlineKeyboardButtonData("✅ Да, подтвердить 23:59", "fsm:date_only:ok")
			btnBack := tgbotapi.NewInlineKeyboardButtonData("◀️ Изменить дату", "fsm:back")
			btnCancel := tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "fsm:cancel")

			reply := tgbotapi.NewMessage(msg.Chat.ID, prompt)
			reply.ParseMode = tgbotapi.ModeHTML
			reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(btnOk),
				tgbotapi.NewInlineKeyboardRow(btnBack, btnCancel),
			)
			_, err = b.api.Send(reply)
			return err
		}

		if isPast {
			// Past deadline confirmation
			draft.ConfirmedPast = false
			_ = b.fsm.SetState(ctx, user.ID, "create_lab", "confirm_past", draft)

			formatted := b.timeHelper.FormatInGroupTZ(dl)
			prompt := fmt.Sprintf(
				"⚠️ <b>Внимание: указанный дедлайн (%s) уже в прошлом!</b>\n\n"+
					"Прошедший дедлайн допускается только после отдельного подтверждения администратора.\n\n"+
					"Вы уверены, что хотите установить прошедший дедлайн?",
				formatted,
			)
			btnConfirm := tgbotapi.NewInlineKeyboardButtonData("✅ Да, подтверждаю", "fsm:past:ok")
			btnBack := tgbotapi.NewInlineKeyboardButtonData("◀️ Изменить дату", "fsm:back")
			btnCancel := tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "fsm:cancel")

			reply := tgbotapi.NewMessage(msg.Chat.ID, prompt)
			reply.ParseMode = tgbotapi.ModeHTML
			reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(btnConfirm),
				tgbotapi.NewInlineKeyboardRow(btnBack, btnCancel),
			)
			_, err = b.api.Send(reply)
			return err
		}

		// Proceed to materials step
		return b.askLabMaterials(msg.Chat.ID, user.ID, draft)

	case "materials":
		// User can send link as text, or send a document/file
		if msg.Document != nil {
			draft.FileID = msg.Document.FileID
			draft.FileName = msg.Document.FileName
		} else if strings.TrimSpace(msg.Text) != "" {
			txt := strings.TrimSpace(msg.Text)
			u, err := url.ParseRequestURI(txt)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				prompt := "❌ Некорректная ссылка на задание. Ссылка должна начинаться с http:// или https://\nИли прикрепите файл документом, либо нажмите «Пропустить»:"
				reply := tgbotapi.NewMessage(msg.Chat.ID, prompt)
				reply.ReplyMarkup = FSMControls(true, true)
				_, err = b.api.Send(reply)
				return err
			}
			draft.SubmissionURL = txt
		}

		return b.askLabSubmissionMethod(msg.Chat.ID, user.ID, draft)

	case "submission_method":
		draft.SubmissionMethod = strings.TrimSpace(msg.Text)
		return b.showLabPreviewAndConfirm(msg.Chat.ID, user.ID, draft)
	}

	return nil
}

func (b *Bot) askLabMaterials(chatID int64, userID int64, draft *LabDraft) error {
	ctx := context.Background()
	_ = b.fsm.SetState(ctx, userID, "create_lab", "materials", draft)

	prompt := "<b>Шаг 5 из 7: Материалы и задание (необязательно)</b>\n\n" +
		"Вы можете:\n" +
		"• Отправить ссылку на задание (начинается с http:// или https://)\n" +
		"• Или отправить файл (документ, архив, методичку) в сообщении\n\n" +
		"Либо нажмите «Пропустить»:"
	reply := tgbotapi.NewMessage(chatID, prompt)
	reply.ParseMode = tgbotapi.ModeHTML
	reply.ReplyMarkup = FSMControls(true, true)
	_, err := b.api.Send(reply)
	return err
}

func (b *Bot) askLabSubmissionMethod(chatID int64, userID int64, draft *LabDraft) error {
	ctx := context.Background()
	_ = b.fsm.SetState(ctx, userID, "create_lab", "submission_method", draft)

	prompt := "<b>Шаг 6 из 7: Способ сдачи и комментарий преподавателя (необязательно)</b>\n\n" +
		"Пример: <code>Сдача в ауд. 402, защита кода у доски</code>\n\n" +
		"Либо нажмите «Пропустить»:"
	reply := tgbotapi.NewMessage(chatID, prompt)
	reply.ParseMode = tgbotapi.ModeHTML
	reply.ReplyMarkup = FSMControls(true, true)
	_, err := b.api.Send(reply)
	return err
}

func (b *Bot) showLabPreviewAndConfirm(chatID int64, userID int64, draft *LabDraft) error {
	ctx := context.Background()
	_ = b.fsm.SetState(ctx, userID, "create_lab", "confirm", draft)

	deadlineStr := b.timeHelper.FormatInGroupTZ(draft.DeadlineAt)

	var sb strings.Builder
	sb.WriteString("📋 <b>Шаг 7 из 7: Проверка карточки перед сохранением</b>\n\n")
	sb.WriteString(fmt.Sprintf("📚 <b>Предмет:</b> %s\n", html.EscapeString(draft.SubjectName)))
	sb.WriteString(fmt.Sprintf("📝 <b>Работа:</b> №%s %s\n", html.EscapeString(draft.Number), html.EscapeString(draft.Title)))
	sb.WriteString(fmt.Sprintf("⏰ <b>Дедлайн:</b> %s\n", deadlineStr))

	if draft.Description != "" {
		sb.WriteString(fmt.Sprintf("ℹ️ <b>Описание:</b> %s\n", html.EscapeString(draft.Description)))
	}
	if draft.SubmissionURL != "" {
		sb.WriteString(fmt.Sprintf("🔗 <b>Ссылка:</b> %s\n", html.EscapeString(draft.SubmissionURL)))
	}
	if draft.FileID != "" {
		fName := draft.FileName
		if fName == "" {
			fName = "Прикрепленный файл"
		}
		sb.WriteString(fmt.Sprintf("📎 <b>Файл:</b> %s\n", html.EscapeString(fName)))
	}
	if draft.SubmissionMethod != "" {
		sb.WriteString(fmt.Sprintf("📬 <b>Способ сдачи:</b> %s\n", html.EscapeString(draft.SubmissionMethod)))
	}

	btnSave := tgbotapi.NewInlineKeyboardButtonData("✅ Сохранить и опубликовать", "fsm:lab:save")
	btnBack := tgbotapi.NewInlineKeyboardButtonData("◀️ Назад", "fsm:back")
	btnCancel := tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "fsm:cancel")

	reply := tgbotapi.NewMessage(chatID, sb.String())
	reply.ParseMode = tgbotapi.ModeHTML
	reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(btnSave),
		tgbotapi.NewInlineKeyboardRow(btnBack, btnCancel),
	)
	_, err := b.api.Send(reply)
	return err
}

func (b *Bot) handleFSMRescheduleMessage(ctx context.Context, msg *tgbotapi.Message, user *domain.User, ds *domain.DialogueState) error {
	draft, err := b.fsm.GetRescheduleDraft(ds)
	if err != nil {
		_ = b.fsm.Clear(ctx, user.ID)
		return b.sendError(msg.Chat.ID, "Ошибка чтения данных. Повторите попытку.")
	}

	dl, dateOnly, err := b.timeHelper.ParseDeadline(msg.Text)
	if err != nil {
		reply := tgbotapi.NewMessage(msg.Chat.ID, fmt.Sprintf("❌ %s\nПожалуйста, введите дату снова:", err.Error()))
		reply.ReplyMarkup = FSMControls(false, false)
		_, err = b.api.Send(reply)
		return err
	}

	draft.NewDeadlineAt = dl
	now := b.clock.Now()
	isPast := dl.Before(now)

	if dateOnly || isPast {
		_ = b.fsm.SetState(ctx, user.ID, "reschedule_lab", "confirm", draft)
		formatted := b.timeHelper.FormatInGroupTZ(dl)
		prompt := fmt.Sprintf(
			"ℹ️ Новый дедлайн: <b>%s</b>\n\n", formatted,
		)
		if isPast {
			prompt += "⚠️ <b>Внимание: дедлайн находится в прошлом!</b>\nПодтвердите перенос на прошедшую дату:"
		} else {
			prompt += "Подтвердите перенос срока сдачи:"
		}

		btnConfirm := tgbotapi.NewInlineKeyboardButtonData("✅ Подтвердить перенос", "fsm:rsch:ok")
		btnCancel := tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "fsm:cancel")

		reply := tgbotapi.NewMessage(msg.Chat.ID, prompt)
		reply.ParseMode = tgbotapi.ModeHTML
		reply.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(btnConfirm, btnCancel),
		)
		_, err = b.api.Send(reply)
		return err
	}

	// Directly reschedule if future
	oldDL, newDL, newVer, err := b.labService.RescheduleLabDeadline(ctx, user.ID, draft.LabID, dl, true)
	if err != nil {
		return b.sendError(msg.Chat.ID, fmt.Sprintf("Ошибка переноса: %s", err.Error()))
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
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = tgbotapi.ModeHTML
	_, err = b.api.Send(reply)
	return err
}

func (b *Bot) handleFSMCreateSubject(ctx context.Context, msg *tgbotapi.Message, user *domain.User) error {
	name := strings.TrimSpace(msg.Text)
	if name == "" {
		return b.sendError(msg.Chat.ID, "Название предмета не может быть пустым.")
	}
	if user.GroupID == nil {
		return b.sendError(msg.Chat.ID, "Вы не подключены к группе.")
	}

	sub, err := b.subjectService.CreateSubject(ctx, user.ID, *user.GroupID, name)
	if err != nil {
		return b.sendError(msg.Chat.ID, fmt.Sprintf("Ошибка создания предмета: %s", err.Error()))
	}
	_ = b.fsm.Clear(ctx, user.ID)

	text := fmt.Sprintf("✅ Предмет <b>«%s»</b> успешно создан!", html.EscapeString(sub.Name))
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = tgbotapi.ModeHTML
	_, err = b.api.Send(reply)
	return err
}

func (b *Bot) handleFSMRenameSubject(ctx context.Context, msg *tgbotapi.Message, user *domain.User, ds *domain.DialogueState) error {
	var subID int64
	_, err := fmt.Sscanf(ds.Step, "%d", &subID)
	if err != nil || subID == 0 {
		_ = b.fsm.Clear(ctx, user.ID)
		return b.sendError(msg.Chat.ID, "Некорректный ID предмета.")
	}

	newName := strings.TrimSpace(msg.Text)
	if newName == "" {
		return b.sendError(msg.Chat.ID, "Название предмета не может быть пустым.")
	}

	if err := b.subjectService.RenameSubject(ctx, user.ID, subID, newName); err != nil {
		return b.sendError(msg.Chat.ID, fmt.Sprintf("Ошибка переименования: %s", err.Error()))
	}
	_ = b.fsm.Clear(ctx, user.ID)

	text := fmt.Sprintf("✅ Предмет успешно переименован в <b>«%s»</b>!", html.EscapeString(newName))
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = tgbotapi.ModeHTML
	_, err = b.api.Send(reply)
	return err
}

func (b *Bot) handleFSMGroupSetup(ctx context.Context, msg *tgbotapi.Message, user *domain.User, ds *domain.DialogueState) error {
	draft, err := b.fsm.GetGroupSetupDraft(ds)
	if err != nil {
		_ = b.fsm.Clear(ctx, user.ID)
		return b.sendError(msg.Chat.ID, "Ошибка чтения настроек группы.")
	}

	switch ds.Step {
	case "name":
		name := strings.TrimSpace(msg.Text)
		if name == "" {
			return b.sendError(msg.Chat.ID, "Название группы не может быть пустым.")
		}
		draft.GroupName = name
		_ = b.fsm.SetState(ctx, user.ID, "group_setup", "code", draft)

		prompt := fmt.Sprintf("Название группы: <b>%s</b>\nТеперь введите новый <b>код приглашения</b> для студентов:", html.EscapeString(name))
		reply := tgbotapi.NewMessage(msg.Chat.ID, prompt)
		reply.ParseMode = tgbotapi.ModeHTML
		reply.ReplyMarkup = FSMControls(false, false)
		_, err := b.api.Send(reply)
		return err

	case "code":
		code := strings.TrimSpace(msg.Text)
		if code == "" {
			return b.sendError(msg.Chat.ID, "Код приглашения не может быть пустым.")
		}
		draft.InviteCode = code

		if err := b.userService.SetupGroup(ctx, user.ID, draft.GroupID, draft.GroupName, draft.InviteCode); err != nil {
			return b.sendError(msg.Chat.ID, fmt.Sprintf("Ошибка сохранения настроек: %s", err.Error()))
		}
		_ = b.fsm.Clear(ctx, user.ID)

		text := fmt.Sprintf(
			"✅ <b>Настройки группы сохранены!</b>\n\n"+
				"Название: <b>%s</b>\n"+
				"Код приглашения: <code>%s</code>",
			html.EscapeString(draft.GroupName), html.EscapeString(draft.InviteCode),
		)
		reply := tgbotapi.NewMessage(msg.Chat.ID, text)
		reply.ParseMode = tgbotapi.ModeHTML
		_, err := b.api.Send(reply)
		return err
	}
	return nil
}

func (b *Bot) sendError(chatID int64, text string) error {
	reply := tgbotapi.NewMessage(chatID, text)
	_, err := b.api.Send(reply)
	return err
}

// resumeFSMDialogue restores the FSM dialogue from the saved state
func (b *Bot) resumeFSMDialogue(ctx context.Context, cb *tgbotapi.CallbackQuery, user *domain.User, ds *domain.DialogueState) error {
	chatID := cb.Message.Chat.ID
	messageID := cb.Message.MessageID

	switch ds.State {
	case "enter_invite":
		edit := tgbotapi.NewEditMessageText(chatID, messageID, "👋 <b>Продолжаем ввод кода приглашения</b>\n\nПожалуйста, введите код приглашения:")
		edit.ParseMode = tgbotapi.ModeHTML
		markup := FSMControls(false, false)
		edit.ReplyMarkup = &markup
		_, err := b.api.Send(edit)
		return err

	case "create_lab":
		draft, err := b.fsm.GetLabDraft(ds)
		if err != nil {
			_ = b.fsm.Clear(ctx, user.ID)
			edit := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка чтения черновика. Начните заново.")
			_, err := b.api.Send(edit)
			return err
		}

		var prompt string
		switch ds.Step {
		case "number_title":
			prompt = "<b>Шаг 2 из 7: Введите номер и название работы</b>\nПример: <code>1. Основы синтаксиса Go</code>"
		case "description":
			prompt = "<b>Шаг 3 из 7: Описание работы</b>\nВведите описание или нажмите «Пропустить»:"
		case "deadline":
			prompt = fmt.Sprintf("<b>Шаг 4 из 7: Дедлайн</b>\nВведите дату и время сдачи (в %s):", b.timeHelper.TimezoneName())
		case "confirm_date_only":
			prompt = "ℹ️ Подтвердите время сдачи 23:59 или измените дату:"
		case "confirm_past":
			prompt = "⚠️ Подтвердите прошедший дедлайн или измените дату:"
		case "materials":
			prompt = "<b>Шаг 5 из 7: Материалы и задание</b>\nОтправьте ссылку или файл, либо нажмите «Пропустить»:"
		case "submission_method":
			prompt = "<b>Шаг 6 из 7: Способ сдачи и комментарий</b>\nВведите текст или нажмите «Пропустить»:"
		case "confirm":
			return b.showLabPreviewAndConfirm(chatID, user.ID, draft)
		default:
			prompt = "<b>Продолжаем создание лабораторной работы</b>\nВведите номер и название работы:"
		}

		edit := tgbotapi.NewEditMessageText(chatID, messageID, prompt)
		edit.ParseMode = tgbotapi.ModeHTML
		markup := FSMControls(true, true)
		edit.ReplyMarkup = &markup
		_, err = b.api.Send(edit)
		return err

	case "reschedule_lab":
		draft, err := b.fsm.GetRescheduleDraft(ds)
		if err != nil {
			_ = b.fsm.Clear(ctx, user.ID)
			edit := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка чтения данных. Начните заново.")
			_, err := b.api.Send(edit)
			return err
		}

		prompt := fmt.Sprintf(
			"<b>Продолжаем перенос дедлайна</b>\n\n"+
				"Работа: <b>%s</b>\n"+
				"Введите новую дату и время сдачи (в %s):",
			html.EscapeString(draft.LabTitle), b.timeHelper.TimezoneName(),
		)
		edit := tgbotapi.NewEditMessageText(chatID, messageID, prompt)
		edit.ParseMode = tgbotapi.ModeHTML
		markup := FSMControls(false, false)
		edit.ReplyMarkup = &markup
		_, err = b.api.Send(edit)
		return err

	case "create_sub":
		edit := tgbotapi.NewEditMessageText(chatID, messageID, "<b>Продолжаем создание предмета</b>\nВведите название нового предмета:")
		edit.ParseMode = tgbotapi.ModeHTML
		markup := FSMControls(false, false)
		edit.ReplyMarkup = &markup
		_, err := b.api.Send(edit)
		return err

	case "rename_sub":
		var subID int64
		_, err := fmt.Sscanf(ds.Step, "%d", &subID)
		if err != nil || subID == 0 {
			_ = b.fsm.Clear(ctx, user.ID)
			edit := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Некорректный ID предмета. Начните заново.")
			_, err := b.api.Send(edit)
			return err
		}
		edit := tgbotapi.NewEditMessageText(chatID, messageID, "<b>Продолжаем переименование предмета</b>\nВведите новое название:")
		edit.ParseMode = tgbotapi.ModeHTML
		markup := FSMControls(false, false)
		edit.ReplyMarkup = &markup
		_, err = b.api.Send(edit)
		return err

	case "group_setup":
		draft, err := b.fsm.GetGroupSetupDraft(ds)
		if err != nil {
			_ = b.fsm.Clear(ctx, user.ID)
			edit := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Ошибка чтения настроек. Начните заново.")
			_, err := b.api.Send(edit)
			return err
		}

		var prompt string
		if ds.Step == "name" {
			prompt = "<b>Продолжаем настройку группы</b>\nВведите новое название учебной группы:"
		} else {
			prompt = "<b>Продолжаем настройку группы</b>\nВведите новый код приглашения для студентов:"
		}
		_ = draft // used in prompt above
		edit := tgbotapi.NewEditMessageText(chatID, messageID, prompt)
		edit.ParseMode = tgbotapi.ModeHTML
		markup := FSMControls(false, false)
		edit.ReplyMarkup = &markup
		_, err = b.api.Send(edit)
		return err

	default:
		_ = b.fsm.Clear(ctx, user.ID)
		edit := tgbotapi.NewEditMessageText(chatID, messageID, "❌ Неизвестное состояние. Начните заново.")
		_, err := b.api.Send(edit)
		return err
	}
}
