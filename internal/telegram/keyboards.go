package telegram

import (
	"fmt"

	"deadline_bot/internal/domain"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// MainMenuKeyboard returns the persistent bottom reply keyboard.
func MainMenuKeyboard(isAdminOrOwner bool) tgbotapi.ReplyKeyboardMarkup {
	row1 := tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("📅 Ближайшие дедлайны"),
		tgbotapi.NewKeyboardButton("🗓 На этой неделе"),
	)
	row2 := tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("📚 Предметы"),
		tgbotapi.NewKeyboardButton("⚠️ Просроченные"),
	)
	row3 := tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("✅ Сданные"),
		tgbotapi.NewKeyboardButton("⚙️ Настройки"),
	)

	keyboard := tgbotapi.NewReplyKeyboard(row1, row2, row3)
	if isAdminOrOwner {
		keyboard.Keyboard = append(keyboard.Keyboard, tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton("⚡ Панель администратора"),
		))
	}
	keyboard.ResizeKeyboard = true
	return keyboard
}

// SettingsKeyboard renders toggle buttons for notification settings.
func SettingsKeyboard(s *domain.UserSettings, tzName string) tgbotapi.InlineKeyboardMarkup {
	icon := func(b bool) string {
		if b {
			return "✅ Вкл"
		}
		return "❌ Выкл"
	}

	btnAll := tgbotapi.NewInlineKeyboardButtonData(
		fmt.Sprintf("🔔 Все напоминания: %s", icon(s.RemindAll)),
		"set:toggle:all",
	)
	btn3d := tgbotapi.NewInlineKeyboardButtonData(
		fmt.Sprintf("3️⃣ За 3 дня: %s", icon(s.Remind3d)),
		"set:toggle:3d",
	)
	btn1d := tgbotapi.NewInlineKeyboardButtonData(
		fmt.Sprintf("1️⃣ За 1 день: %s", icon(s.Remind1d)),
		"set:toggle:1d",
	)
	btn3h := tgbotapi.NewInlineKeyboardButtonData(
		fmt.Sprintf("⏰ За 3 часа: %s", icon(s.Remind3h)),
		"set:toggle:3h",
	)
	btnNew := tgbotapi.NewInlineKeyboardButtonData(
		fmt.Sprintf("🆕 Новые работы: %s", icon(s.NotifyNewLab)),
		"set:toggle:new_lab",
	)
	btnChg := tgbotapi.NewInlineKeyboardButtonData(
		fmt.Sprintf("🔄 Переносы и отмены: %s", icon(s.NotifyChanges)),
		"set:toggle:changes",
	)

	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(btnAll),
		tgbotapi.NewInlineKeyboardRow(btn3d, btn1d),
		tgbotapi.NewInlineKeyboardRow(btn3h),
		tgbotapi.NewInlineKeyboardRow(btnNew),
		tgbotapi.NewInlineKeyboardRow(btnChg),
	)
}

// LabCardKeyboard renders buttons for a specific lab card.
func LabCardKeyboard(lab *domain.Lab, backCallback string) tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton

	// Completion toggle button
	if lab.IsCompleted {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("↩️ Снять отметку о сдаче", fmt.Sprintf("lab:undone:%d", lab.ID)),
		))
	} else {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Отметить как сданную", fmt.Sprintf("lab:done:%d", lab.ID)),
		))
	}

	// Materials buttons
	var matRow []tgbotapi.InlineKeyboardButton
	if lab.SubmissionURL != "" {
		matRow = append(matRow, tgbotapi.NewInlineKeyboardButtonURL("🔗 Открыть задание", lab.SubmissionURL))
	}
	if lab.FileID != "" {
		matRow = append(matRow, tgbotapi.NewInlineKeyboardButtonData("📎 Получить файл", fmt.Sprintf("lab:file:%d", lab.ID)))
	}
	if len(matRow) > 0 {
		rows = append(rows, matRow)
	}

	if backCallback != "" {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Назад к списку", backCallback),
		))
	}

	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// PaginationKeyboard creates previous / page indicator / next inline buttons.
func PaginationKeyboard(prefix string, page, totalPages int) []tgbotapi.InlineKeyboardButton {
	var row []tgbotapi.InlineKeyboardButton
	if page > 1 {
		row = append(row, tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад", fmt.Sprintf("p:%s:%d", prefix, page-1)))
	}
	row = append(row, tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("Стр. %d/%d", page, totalPages), "noop"))
	if page < totalPages {
		row = append(row, tgbotapi.NewInlineKeyboardButtonData("Вперед ➡️", fmt.Sprintf("p:%s:%d", prefix, page+1)))
	}
	return row
}

// FSMControls creates Back, Skip, Cancel buttons for dialogue steps.
func FSMControls(showBack bool, showSkip bool) tgbotapi.InlineKeyboardMarkup {
	var row []tgbotapi.InlineKeyboardButton
	if showBack {
		row = append(row, tgbotapi.NewInlineKeyboardButtonData("◀️ Назад", "fsm:back"))
	}
	if showSkip {
		row = append(row, tgbotapi.NewInlineKeyboardButtonData("⏭ Пропустить", "fsm:skip"))
	}
	row = append(row, tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "fsm:cancel"))
	return tgbotapi.NewInlineKeyboardMarkup(row)
}

// AdminMenuKeyboard renders the main admin panel.
func AdminMenuKeyboard(isOwner bool) tgbotapi.InlineKeyboardMarkup {
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Создать лабораторную", "adm:lab:new"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Управление работами", "adm:labs:1"),
			tgbotapi.NewInlineKeyboardButtonData("📚 Предметы", "adm:subs"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("👥 Студенты и доступ", "adm:users:1"),
			tgbotapi.NewInlineKeyboardButtonData("🔑 Код приглашения", "adm:code"),
		),
	}

	if isOwner {
		rows = append(rows,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("👑 Управление администраторами", "adm:admins"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⚙️ Настройки группы", "adm:grp:setup"),
			),
		)
	}

	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}
