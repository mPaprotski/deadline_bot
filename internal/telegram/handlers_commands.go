package telegram

import (
	"context"
	"fmt"
	"html"

	"deadline_bot/internal/domain"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *Bot) handleCommand(ctx context.Context, msg *tgbotapi.Message, user *domain.User) error {
	switch msg.Command() {
	case "start":
		return b.cmdStart(ctx, msg, user)
	case "deadlines":
		return b.cmdDeadlines(ctx, msg, user)
	case "subjects":
		return b.cmdSubjects(ctx, msg, user)
	case "settings":
		return b.cmdSettings(ctx, msg, user)
	case "help":
		return b.cmdHelp(ctx, msg, user)
	case "admin":
		return b.cmdAdmin(ctx, msg, user)
	default:
		reply := tgbotapi.NewMessage(msg.Chat.ID, "Неизвестная команда. Введите /help для получения справки.")
		_, err := b.api.Send(reply)
		return err
	}
}

func (b *Bot) cmdStart(ctx context.Context, msg *tgbotapi.Message, user *domain.User) error {
	// If user is revoked:
	if user.Status == domain.UserStatusRevoked {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "⛔ <b>Доступ к группе отозван.</b>\nОбратитесь к старосте или администратору группы для восстановления доступа.")
		reply.ParseMode = tgbotapi.ModeHTML
		_, err := b.api.Send(reply)
		return err
	}

	// If user is not yet connected to a group:
	if user.GroupID == nil {
		// If user is the owner, check if a group exists
		if user.Role == domain.RoleOwner {
			grp, err := b.userService.EnsureDefaultGroup(ctx, "Учебная группа", "STUDENT2026")
			if err == nil && grp != nil {
				text := fmt.Sprintf(
					"👑 <b>Здравствуйте, староста (владелец бота)!</b>\n\n"+
						"Ваша группа создана: <b>%s</b>\n"+
						"🔑 Код приглашения для студентов: <code>%s</code>\n\n"+
						"Студенты могут запустить бота и ввести этот код для подключения.\n"+
						"Вы можете изменить название группы и код в /admin.",
					html.EscapeString(grp.Name), html.EscapeString(grp.InviteCode),
				)
				reply := tgbotapi.NewMessage(msg.Chat.ID, text)
				reply.ParseMode = tgbotapi.ModeHTML
				reply.ReplyMarkup = MainMenuKeyboard(true)
				_, err = b.api.Send(reply)
				return err
			}
		}

		// Prompt for invite code
		_ = b.fsm.SetState(ctx, user.ID, "enter_invite", "input", nil)
		text := "👋 <b>Добро пожаловать в бот дедлайнов учебной группы!</b>\n\n" +
			"Бот уведомляет студентов о сроках сдачи лабораторных работ и присылает автоматические напоминания за 3 дня, 1 день и 3 часа.\n\n" +
			"Чтобы подключиться к вашей группе, пожалуйста, <b>введите код приглашения</b> (его можно получить у старосты):"
		reply := tgbotapi.NewMessage(msg.Chat.ID, text)
		reply.ParseMode = tgbotapi.ModeHTML
		_, err := b.api.Send(reply)
		return err
	}

	// User is already connected to group
	grp, err := b.userService.GetGroup(ctx, *user.GroupID)
	grpName := "Учебная группа"
	if err == nil && grp != nil {
		grpName = grp.Name
	}

	text := fmt.Sprintf(
		"👋 Здравствуйте, <b>%s</b>!\n\n"+
			"Группа: <b>%s</b>\n"+
			"Часовой пояс: <b>%s</b>\n\n"+
			"Выберите интересующий раздел в меню ниже:",
		html.EscapeString(user.FullName()), html.EscapeString(grpName), b.timeHelper.TimezoneName(),
	)
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = tgbotapi.ModeHTML
	reply.ReplyMarkup = MainMenuKeyboard(user.IsAdminOrOwner())
	_, err = b.api.Send(reply)
	return err
}

func (b *Bot) cmdDeadlines(ctx context.Context, msg *tgbotapi.Message, user *domain.User) error {
	if user.GroupID == nil || user.Status != domain.UserStatusActive {
		return b.sendInvitePrompt(msg.Chat.ID)
	}
	return b.showUpcomingDeadlines(ctx, msg.Chat.ID, 0, user.ID, *user.GroupID, 1)
}

func (b *Bot) cmdSubjects(ctx context.Context, msg *tgbotapi.Message, user *domain.User) error {
	if user.GroupID == nil || user.Status != domain.UserStatusActive {
		return b.sendInvitePrompt(msg.Chat.ID)
	}
	return b.showSubjects(ctx, msg.Chat.ID, 0, *user.GroupID)
}

func (b *Bot) cmdSettings(ctx context.Context, msg *tgbotapi.Message, user *domain.User) error {
	if user.GroupID == nil || user.Status != domain.UserStatusActive {
		return b.sendInvitePrompt(msg.Chat.ID)
	}
	return b.showSettings(ctx, msg.Chat.ID, 0, user.ID)
}

func (b *Bot) cmdHelp(_ context.Context, msg *tgbotapi.Message, _ *domain.User) error {
	text := "📖 <b>Справка по использованию бота:</b>\n\n" +
		"<b>Для студентов:</b>\n" +
		"• 📅 <b>Ближайшие дедлайны</b> — актуальные несданные работы с будущим сроком сдачи.\n" +
		"• 🗓 <b>На этой неделе</b> — работы с дедлайном в текущую календарную неделю (Пн–Вскр).\n" +
		"• 📚 <b>Предметы</b> — список предметов и просмотр работ по каждому из них.\n" +
		"• ⚠️ <b>Просроченные</b> — несданные работы с наступившим дедлайном.\n" +
		"• ✅ <b>Сданные</b> — работы, отмеченные вами как сданные.\n" +
		"• ⚙️ <b>Настройки</b> — включение/выключение напоминаний (за 3 дня, 1 день, 3 часа) и оповещений о новых работах.\n\n" +
		"<i>Примечание:</i> личная отметка «Сдано» помогает организовать ваше расписание и прекращает напоминания по этой работе, но не подтверждает сдачу преподавателю.\n\n" +
		"<b>Для администраторов (старосты и заместителя):</b>\n" +
		"• /admin — панель управления предметами, лабораторными работами и доступом студентов.\n\n" +
		fmt.Sprintf("🕒 Часовой пояс группы: <b>%s</b>", b.timeHelper.TimezoneName())

	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = tgbotapi.ModeHTML
	_, err := b.api.Send(reply)
	return err
}

func (b *Bot) cmdAdmin(ctx context.Context, msg *tgbotapi.Message, user *domain.User) error {
	if !user.IsAdminOrOwner() {
		reply := tgbotapi.NewMessage(msg.Chat.ID, "⛔ У вас нет прав администратора.")
		_, err := b.api.Send(reply)
		return err
	}
	return b.showAdminMenu(ctx, msg.Chat.ID, 0, user.IsOwner())
}

func (b *Bot) sendInvitePrompt(chatID int64) error {
	reply := tgbotapi.NewMessage(chatID, "Для доступа к функциям бота необходимо ввести код приглашения в группу. Введите /start для начала.")
	_, err := b.api.Send(reply)
	return err
}
