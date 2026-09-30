package telegram

import (
	"context"
	"fmt"
	"html"
	"strings"

	"deadline_bot/internal/domain"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const pageSize = 5

func (b *Bot) sendOrEdit(chatID int64, msgID int, text string, markup *tgbotapi.InlineKeyboardMarkup) error {
	if msgID != 0 {
		edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
		edit.ParseMode = tgbotapi.ModeHTML
		edit.DisableWebPagePreview = true
		if markup != nil {
			edit.ReplyMarkup = markup
		}
		_, err := b.api.Send(edit)
		if err == nil {
			return nil
		}
	}

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true
	if markup != nil {
		msg.ReplyMarkup = markup
	}
	_, err := b.api.Send(msg)
	return err
}

func (b *Bot) showUpcomingDeadlines(ctx context.Context, chatID int64, msgID int, userID int64, groupID int64, page int) error {
	offset := (page - 1) * pageSize
	labs, total, err := b.labService.ListUpcoming(ctx, groupID, userID, pageSize, offset)
	if err != nil {
		return b.sendOrEdit(chatID, msgID, fmt.Sprintf("Ошибка загрузки дедлайнов: %s", err.Error()), nil)
	}

	if total == 0 {
		return b.sendOrEdit(chatID, msgID, "🎉 <b>Нет актуальных дедлайнов!</b>\nВсе работы сданы или еще не назначены.", nil)
	}

	totalPages := (total + pageSize - 1) / pageSize
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📅 <b>Ближайшие дедлайны</b> (всего: %d)\n\n", total))

	var rows [][]tgbotapi.InlineKeyboardButton
	now := b.clock.Now()
	for i, lab := range labs {
		rem, _ := b.timeHelper.RemainingOrOverdue(lab.DeadlineAt, now)
		sb.WriteString(fmt.Sprintf("<b>%d.</b> [%s] №%s %s\n", offset+i+1, html.EscapeString(lab.SubjectName), html.EscapeString(lab.Number), html.EscapeString(lab.Title)))
		sb.WriteString(fmt.Sprintf("    ⏰ %s (%s)\n\n", b.timeHelper.FormatShortInGroupTZ(lab.DeadlineAt), rem))

		btnText := fmt.Sprintf("📖 №%s %s", lab.Number, lab.Title)
		if len([]rune(btnText)) > 30 {
			btnText = string([]rune(btnText)[:27]) + "..."
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, fmt.Sprintf("lab:v:%d", lab.ID)),
		))
	}

	if totalPages > 1 {
		rows = append(rows, PaginationKeyboard("deadlines", page, totalPages))
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
}

func (b *Bot) showWeekDeadlines(ctx context.Context, chatID int64, msgID int, userID int64, groupID int64, page int) error {
	offset := (page - 1) * pageSize
	labs, total, err := b.labService.ListThisWeek(ctx, groupID, userID, pageSize, offset)
	if err != nil {
		return b.sendOrEdit(chatID, msgID, fmt.Sprintf("Ошибка: %s", err.Error()), nil)
	}

	if total == 0 {
		return b.sendOrEdit(chatID, msgID, "🗓 <b>На этой неделе дедлайнов нет!</b>\nОтличная возможность отдохнуть или закрыть долги.", nil)
	}

	totalPages := (total + pageSize - 1) / pageSize
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🗓 <b>Дедлайны на этой неделе</b> (всего: %d)\n\n", total))

	var rows [][]tgbotapi.InlineKeyboardButton
	now := b.clock.Now()
	for i, lab := range labs {
		statusBadge := "⏳"
		if lab.IsCompleted {
			statusBadge = "✅ Сдано"
		}
		rem, _ := b.timeHelper.RemainingOrOverdue(lab.DeadlineAt, now)
		sb.WriteString(fmt.Sprintf("<b>%d.</b> %s [%s] №%s %s\n", offset+i+1, statusBadge, html.EscapeString(lab.SubjectName), html.EscapeString(lab.Number), html.EscapeString(lab.Title)))
		sb.WriteString(fmt.Sprintf("    ⏰ %s (%s)\n\n", b.timeHelper.FormatShortInGroupTZ(lab.DeadlineAt), rem))

		btnText := fmt.Sprintf("📖 №%s %s", lab.Number, lab.Title)
		if len([]rune(btnText)) > 30 {
			btnText = string([]rune(btnText)[:27]) + "..."
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, fmt.Sprintf("lab:v:%d", lab.ID)),
		))
	}

	if totalPages > 1 {
		rows = append(rows, PaginationKeyboard("week", page, totalPages))
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
}

func (b *Bot) showOverdueDeadlines(ctx context.Context, chatID int64, msgID int, userID int64, groupID int64, page int) error {
	offset := (page - 1) * pageSize
	labs, total, err := b.labService.ListOverdue(ctx, groupID, userID, pageSize, offset)
	if err != nil {
		return b.sendOrEdit(chatID, msgID, fmt.Sprintf("Ошибка: %s", err.Error()), nil)
	}

	if total == 0 {
		return b.sendOrEdit(chatID, msgID, "🎉 <b>Нет просроченных работ!</b>\nВы идете в ногу со сроками.", nil)
	}

	totalPages := (total + pageSize - 1) / pageSize
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("⚠️ <b>Просроченные работы</b> (всего: %d)\n\n", total))

	var rows [][]tgbotapi.InlineKeyboardButton
	now := b.clock.Now()
	for i, lab := range labs {
		rem, _ := b.timeHelper.RemainingOrOverdue(lab.DeadlineAt, now)
		sb.WriteString(fmt.Sprintf("<b>%d.</b> [%s] №%s %s\n", offset+i+1, html.EscapeString(lab.SubjectName), html.EscapeString(lab.Number), html.EscapeString(lab.Title)))
		sb.WriteString(fmt.Sprintf("    ⏰ Дедлайн был: %s (%s)\n\n", b.timeHelper.FormatShortInGroupTZ(lab.DeadlineAt), rem))

		btnText := fmt.Sprintf("📖 №%s %s", lab.Number, lab.Title)
		if len([]rune(btnText)) > 30 {
			btnText = string([]rune(btnText)[:27]) + "..."
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, fmt.Sprintf("lab:v:%d", lab.ID)),
		))
	}

	if totalPages > 1 {
		rows = append(rows, PaginationKeyboard("overdue", page, totalPages))
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
}

func (b *Bot) showCompletedDeadlines(ctx context.Context, chatID int64, msgID int, userID int64, groupID int64, page int) error {
	offset := (page - 1) * pageSize
	labs, total, err := b.labService.ListCompleted(ctx, groupID, userID, pageSize, offset)
	if err != nil {
		return b.sendOrEdit(chatID, msgID, fmt.Sprintf("Ошибка: %s", err.Error()), nil)
	}

	if total == 0 {
		return b.sendOrEdit(chatID, msgID, "📝 <b>Пока нет сданных работ.</b>\nОтмечайте сданные работы в их карточках.", nil)
	}

	totalPages := (total + pageSize - 1) / pageSize
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ <b>Сданные работы</b> (всего: %d)\n\n", total))

	var rows [][]tgbotapi.InlineKeyboardButton
	for i, lab := range labs {
		sb.WriteString(fmt.Sprintf("<b>%d.</b> ✅ [%s] №%s %s\n", offset+i+1, html.EscapeString(lab.SubjectName), html.EscapeString(lab.Number), html.EscapeString(lab.Title)))
		if lab.CompletedAt != nil {
			sb.WriteString(fmt.Sprintf("    Отмечено: %s\n\n", b.timeHelper.FormatShortInGroupTZ(*lab.CompletedAt)))
		} else {
			sb.WriteString("\n")
		}

		btnText := fmt.Sprintf("📖 №%s %s", lab.Number, lab.Title)
		if len([]rune(btnText)) > 30 {
			btnText = string([]rune(btnText)[:27]) + "..."
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, fmt.Sprintf("lab:v:%d", lab.ID)),
		))
	}

	if totalPages > 1 {
		rows = append(rows, PaginationKeyboard("completed", page, totalPages))
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
}

func (b *Bot) showSubjects(ctx context.Context, chatID int64, msgID int, groupID int64) error {
	subs, err := b.subjectService.ListActiveSubjects(ctx, groupID)
	if err != nil {
		return b.sendOrEdit(chatID, msgID, fmt.Sprintf("Ошибка: %s", err.Error()), nil)
	}

	if len(subs) == 0 {
		return b.sendOrEdit(chatID, msgID, "📚 В группе пока нет предметов.", nil)
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, s := range subs {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(s.Name, fmt.Sprintf("sub:v:%d", s.ID)),
		))
	}

	text := "📚 <b>Предметы группы</b>\nВыберите предмет для просмотра списка лабораторных работ:"
	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.sendOrEdit(chatID, msgID, text, &markup)
}

func (b *Bot) showSubjectLabs(ctx context.Context, chatID int64, msgID int, userID int64, subjectID int64, page int) error {
	sub, err := b.subjectService.GetSubject(ctx, subjectID)
	if err != nil || sub == nil {
		return b.sendOrEdit(chatID, msgID, "Предмет не найден.", nil)
	}

	offset := (page - 1) * pageSize
	labs, total, err := b.labService.ListBySubject(ctx, subjectID, userID, pageSize, offset)
	if err != nil {
		return b.sendOrEdit(chatID, msgID, fmt.Sprintf("Ошибка: %s", err.Error()), nil)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📚 <b>Предмет: %s</b>\n", html.EscapeString(sub.Name)))

	if total == 0 {
		sb.WriteString("\nПо этому предмету пока нет назначенных лабораторных работ.")
		btnBack := tgbotapi.NewInlineKeyboardButtonData("◀️ К предметам", "p:sub_back:1")
		markup := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(btnBack))
		return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
	}

	sb.WriteString(fmt.Sprintf("Всего работ: %d\n\n", total))
	totalPages := (total + pageSize - 1) / pageSize
	var rows [][]tgbotapi.InlineKeyboardButton
	now := b.clock.Now()

	for i, lab := range labs {
		badge := "⏳"
		if lab.IsCompleted {
			badge = "✅"
		}
		rem, _ := b.timeHelper.RemainingOrOverdue(lab.DeadlineAt, now)
		sb.WriteString(fmt.Sprintf("<b>%d.</b> %s №%s %s\n", offset+i+1, badge, html.EscapeString(lab.Number), html.EscapeString(lab.Title)))
		sb.WriteString(fmt.Sprintf("    ⏰ Дедлайн: %s (%s)\n\n", b.timeHelper.FormatShortInGroupTZ(lab.DeadlineAt), rem))

		btnText := fmt.Sprintf("📖 №%s %s", lab.Number, lab.Title)
		if len([]rune(btnText)) > 30 {
			btnText = string([]rune(btnText)[:27]) + "..."
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, fmt.Sprintf("lab:v:%d", lab.ID)),
		))
	}

	if totalPages > 1 {
		prefix := fmt.Sprintf("subj_labs:%d", subjectID)
		rows = append(rows, PaginationKeyboard(prefix, page, totalPages))
	}

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
}

func (b *Bot) showLabCard(ctx context.Context, chatID int64, msgID int, userID int64, labID int64, backCallback string) error {
	lab, err := b.labService.GetLab(ctx, labID, userID)
	if err != nil || lab == nil {
		return b.sendOrEdit(chatID, msgID, "Лабораторная работа не найдена.", nil)
	}

	now := b.clock.Now()
	rem, _ := b.timeHelper.RemainingOrOverdue(lab.DeadlineAt, now)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 <b>Лабораторная работа №%s</b>\n\n", html.EscapeString(lab.Number)))
	sb.WriteString(fmt.Sprintf("📚 <b>Предмет:</b> %s\n", html.EscapeString(lab.SubjectName)))
	sb.WriteString(fmt.Sprintf("📝 <b>Название:</b> %s\n", html.EscapeString(lab.Title)))
	sb.WriteString(fmt.Sprintf("⏰ <b>Дедлайн:</b> %s\n", b.timeHelper.FormatInGroupTZ(lab.DeadlineAt)))
	sb.WriteString(fmt.Sprintf("%s\n\n", rem))

	statusText := "⏳ <b>Статус:</b> Не сдано"
	if lab.IsCompleted {
		statusText = "✅ <b>Статус:</b> Сдано (лично вами)"
		if lab.CompletedAt != nil {
			statusText += fmt.Sprintf(" [%s]", b.timeHelper.FormatShortInGroupTZ(*lab.CompletedAt))
		}
	}
	sb.WriteString(fmt.Sprintf("%s\n", statusText))

	if lab.Description != "" {
		sb.WriteString(fmt.Sprintf("\nℹ️ <b>Описание:</b>\n%s\n", html.EscapeString(lab.Description)))
	}
	if lab.SubmissionMethod != "" {
		sb.WriteString(fmt.Sprintf("\n📬 <b>Способ сдачи / комментарий:</b>\n%s\n", html.EscapeString(lab.SubmissionMethod)))
	}
	if lab.TeacherComment != "" {
		sb.WriteString(fmt.Sprintf("\n👨‍🏫 <b>Комментарий преподавателя:</b>\n%s\n", html.EscapeString(lab.TeacherComment)))
	}

	markup := LabCardKeyboard(lab, backCallback)
	return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
}

func (b *Bot) showSettings(ctx context.Context, chatID int64, msgID int, userID int64) error {
	settings, err := b.settingsService.GetSettings(ctx, userID)
	if err != nil {
		return b.sendOrEdit(chatID, msgID, fmt.Sprintf("Ошибка загрузки настроек: %s", err.Error()), nil)
	}

	text := fmt.Sprintf(
		"⚙️ <b>Настройки уведомлений</b>\n\n"+
			"Здесь вы можете настроить получение личных напоминаний о дедлайнах.\n"+
			"Часовой пояс группы: <b>%s</b>\n\n"+
			"Нажмите на кнопку для включения или отключения нужного пункта:",
		b.timeHelper.TimezoneName(),
	)
	markup := SettingsKeyboard(settings, b.timeHelper.TimezoneName())
	return b.sendOrEdit(chatID, msgID, text, &markup)
}

func (b *Bot) showAdminMenu(_ context.Context, chatID int64, msgID int, isOwner bool) error {
	text := "⚡ <b>Панель администратора</b>\n\n" +
		"Выберите действие для управления группой, предметами или работами:"
	markup := AdminMenuKeyboard(isOwner)
	return b.sendOrEdit(chatID, msgID, text, &markup)
}

func (b *Bot) showAdminLabs(ctx context.Context, chatID int64, msgID int, groupID int64, page int) error {
	offset := (page - 1) * pageSize
	labs, total, err := b.labService.ListAllForAdmin(ctx, groupID, pageSize, offset)
	if err != nil {
		return b.sendOrEdit(chatID, msgID, fmt.Sprintf("Ошибка: %s", err.Error()), nil)
	}

	if total == 0 {
		text := "📝 В группе пока нет созданных лабораторных работ."
		btnNew := tgbotapi.NewInlineKeyboardButtonData("➕ Создать работу", "adm:lab:new")
		btnBack := tgbotapi.NewInlineKeyboardButtonData("◀️ В админ-меню", "adm:menu")
		markup := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(btnNew),
			tgbotapi.NewInlineKeyboardRow(btnBack),
		)
		return b.sendOrEdit(chatID, msgID, text, &markup)
	}

	totalPages := (total + pageSize - 1) / pageSize
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📝 <b>Управление работами</b> (всего: %d)\nНажмите на работу для редактирования или переноса:\n\n", total))

	var rows [][]tgbotapi.InlineKeyboardButton
	for i, lab := range labs {
		sb.WriteString(fmt.Sprintf("<b>%d.</b> [%s] №%s %s\n    Дедлайн: %s\n\n",
			offset+i+1, html.EscapeString(lab.SubjectName), html.EscapeString(lab.Number), html.EscapeString(lab.Title),
			b.timeHelper.FormatShortInGroupTZ(lab.DeadlineAt),
		))

		btnText := fmt.Sprintf("⚙️ №%s %s", lab.Number, lab.Title)
		if len([]rune(btnText)) > 30 {
			btnText = string([]rune(btnText)[:27]) + "..."
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, fmt.Sprintf("adm:lab:v:%d", lab.ID)),
		))
	}

	if totalPages > 1 {
		rows = append(rows, PaginationKeyboard("adm_labs", page, totalPages))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◀️ В админ-меню", "adm:menu"),
	))

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
}

func (b *Bot) showAdminLabDetails(ctx context.Context, chatID int64, msgID int, userID int64, labID int64) error {
	lab, err := b.labService.GetLab(ctx, labID, userID)
	if err != nil || lab == nil {
		return b.sendOrEdit(chatID, msgID, "Работа не найдена.", nil)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("⚙️ <b>Управление работой №%s</b>\n\n", html.EscapeString(lab.Number)))
	sb.WriteString(fmt.Sprintf("📚 <b>Предмет:</b> %s\n", html.EscapeString(lab.SubjectName)))
	sb.WriteString(fmt.Sprintf("📝 <b>Название:</b> %s\n", html.EscapeString(lab.Title)))
	sb.WriteString(fmt.Sprintf("⏰ <b>Дедлайн:</b> %s\n", b.timeHelper.FormatInGroupTZ(lab.DeadlineAt)))
	sb.WriteString(fmt.Sprintf("🔢 <b>Версия дедлайна:</b> %d\n", lab.DeadlineVersion))

	btnReschedule := tgbotapi.NewInlineKeyboardButtonData("⏰ Перенести дедлайн", fmt.Sprintf("adm:lab:dl:%d", lab.ID))
	btnCancel := tgbotapi.NewInlineKeyboardButtonData("❌ Отменить работу", fmt.Sprintf("adm:lab:cncl:%d", lab.ID))
	btnBack := tgbotapi.NewInlineKeyboardButtonData("◀️ К списку работ", "adm:labs:1")

	markup := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(btnReschedule),
		tgbotapi.NewInlineKeyboardRow(btnCancel),
		tgbotapi.NewInlineKeyboardRow(btnBack),
	)
	return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
}

func (b *Bot) showAdminSubjects(ctx context.Context, chatID int64, msgID int, groupID int64) error {
	subs, err := b.subjectService.ListAllSubjects(ctx, groupID)
	if err != nil {
		return b.sendOrEdit(chatID, msgID, fmt.Sprintf("Ошибка: %s", err.Error()), nil)
	}

	var sb strings.Builder
	sb.WriteString("📚 <b>Управление предметами</b>\n\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("➕ Добавить предмет", "adm:sub:create"),
	))

	for _, s := range subs {
		status := "🟢 Активен"
		toggleAction := "Архивировать"
		if s.IsArchived {
			status = "📦 В архиве"
			toggleAction = "Разархивировать"
		}
		sb.WriteString(fmt.Sprintf("• <b>%s</b> (%s)\n", html.EscapeString(s.Name), status))

		btnRename := tgbotapi.NewInlineKeyboardButtonData("✏️ Переименовать", fmt.Sprintf("adm:sub:ren:%d", s.ID))
		btnArchive := tgbotapi.NewInlineKeyboardButtonData(toggleAction, fmt.Sprintf("adm:sub:arc:%d", s.ID))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btnRename, btnArchive))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◀️ В админ-меню", "adm:menu"),
	))

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
}

func (b *Bot) showAdminUsers(ctx context.Context, chatID int64, msgID int, groupID int64, page int) error {
	users, err := b.userService.ListGroupUsers(ctx, groupID)
	if err != nil {
		return b.sendOrEdit(chatID, msgID, fmt.Sprintf("Ошибка: %s", err.Error()), nil)
	}

	total := len(users)
	offset := (page - 1) * pageSize
	end := offset + pageSize
	if end > total {
		end = total
	}
	totalPages := (total + pageSize - 1) / pageSize

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("👥 <b>Студенты группы</b> (всего: %d)\n\n", total))

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := offset; i < end; i++ {
		u := users[i]
		roleBadge := "Студент"
		switch u.Role {
		case domain.RoleOwner:
			roleBadge = "👑 Владелец"
		case domain.RoleAdmin:
			roleBadge = "⭐ Админ"
		}

		statusBadge := "🟢 Активен"
		if u.Status == domain.UserStatusRevoked {
			statusBadge = "⛔ Доступ отозван"
		}

		sb.WriteString(fmt.Sprintf("<b>%d.</b> %s (%s, %s)\n", i+1, html.EscapeString(u.FullName()), roleBadge, statusBadge))

		if u.Role != domain.RoleOwner {
			if u.Status == domain.UserStatusActive {
				rows = append(rows, tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("⛔ Отозвать доступ: %s", u.FirstName), fmt.Sprintf("adm:u:rev:%d", u.ID)),
				))
			} else {
				rows = append(rows, tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🟢 Восстановить: %s", u.FirstName), fmt.Sprintf("adm:u:rst:%d", u.ID)),
				))
			}
		}
	}

	if totalPages > 1 {
		rows = append(rows, PaginationKeyboard("adm_users", page, totalPages))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◀️ В админ-меню", "adm:menu"),
	))

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
}

func (b *Bot) showAdminAdmins(ctx context.Context, chatID int64, msgID int, groupID int64) error {
	users, err := b.userService.ListGroupUsers(ctx, groupID)
	if err != nil {
		return b.sendOrEdit(chatID, msgID, fmt.Sprintf("Ошибка: %s", err.Error()), nil)
	}

	var sb strings.Builder
	sb.WriteString("👑 <b>Управление администраторами группы</b>\n\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, u := range users {
		if u.Role == domain.RoleOwner {
			sb.WriteString(fmt.Sprintf("👑 <b>%s</b> — Главный староста (Владелец)\n", html.EscapeString(u.FullName())))
		} else if u.Role == domain.RoleAdmin {
			sb.WriteString(fmt.Sprintf("⭐ <b>%s</b> — Заместитель (Администратор)\n", html.EscapeString(u.FullName())))
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("Разжаловать: %s", u.FirstName), fmt.Sprintf("adm:adm:dem:%d", u.ID)),
			))
		} else if u.Status == domain.UserStatusActive {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("Назначить админом: %s", u.FirstName), fmt.Sprintf("adm:adm:prm:%d", u.ID)),
			))
		}
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◀️ В админ-меню", "adm:menu"),
	))

	markup := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.sendOrEdit(chatID, msgID, sb.String(), &markup)
}
