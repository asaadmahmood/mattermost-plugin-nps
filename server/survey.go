package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/mattermost/mattermost-server/model"
)

const (
	// How often "survey scheduled" emails can be sent to prevent multiple emails from being sent if multiple server
	// upgrades occur within a short time
	MIN_DAYS_BETWEEN_SURVEY_EMAILS = 7

	// How long until a survey occurs after a server upgrade
	DAYS_UNTIL_SURVEY = 21

	// Get admin users up to 100 at a time when sending email notifications
	ADMIN_USERS_PER_PAGE = 100
)

type adminNotice struct {
	Sent       bool
	NextSurvey time.Time
}

// checkForNextSurvey schedules a new NPS survey if a major or minor version change has occurred. Returns whether or
// not a survey was scheduled.
//
// Note that this only sends an email to admins to notify them that a survey has been scheduled. The web app plugin is
// in charge of checking and actually triggering the survey.
func (p *Plugin) checkForNextSurvey(currentVersion semver.Version) bool {
	lastUpgrade := p.getLastServerUpgrade()

	if !shouldScheduleSurvey(currentVersion, lastUpgrade) {
		// No version change
		p.API.LogDebug("No server version change detected. Not scheduling a new survey.")
		return false
	}

	now := time.Now()
	nextSurvey := now.Add(DAYS_UNTIL_SURVEY * 24 * time.Hour)

	if lastUpgrade == nil {
		p.API.LogInfo(fmt.Sprintf("NPS plugin installed. Scheduling NPS survey for %s", nextSurvey.Format("Jan 2, 2006")))
	} else {
		p.API.LogInfo(fmt.Sprintf("Version change detected from %s to %s. Scheduling NPS survey for %s", lastUpgrade.Version, currentVersion, nextSurvey.Format("Jan 2, 2006")))
	}

	if shouldSendAdminNotices(now, lastUpgrade) {
		p.sendAdminNotices(nextSurvey)
	}

	if err := p.storeServerUpgrade(&serverUpgrade{
		Version:   currentVersion,
		Timestamp: now,
	}); err != nil {
		p.API.LogError("Failed to store time of server upgrade. The next NPS survey may not occur.", "err", err)
	}

	return true
}

func shouldScheduleSurvey(currentVersion semver.Version, lastUpgrade *serverUpgrade) bool {
	return lastUpgrade == nil || currentVersion.Major > lastUpgrade.Version.Major || currentVersion.Minor > lastUpgrade.Version.Minor
}

func shouldSendAdminNotices(now time.Time, lastUpgrade *serverUpgrade) bool {
	// Only send a "survey scheduled" email if it has been at least 7 days since the last time we've sent one to
	// prevent spamming admins when multiple upgrades are done within a short period.
	return lastUpgrade == nil || now.Sub(lastUpgrade.Timestamp) >= MIN_DAYS_BETWEEN_SURVEY_EMAILS*24*time.Hour
}

func (p *Plugin) sendAdminNotices(nextSurvey time.Time) {
	admins, err := p.getAdminUsers(ADMIN_USERS_PER_PAGE)
	if err != nil {
		p.API.LogError("Failed to get system admins to send admin notices", "err", err)
		return
	}

	p.sendAdminNoticeEmails(admins)
	p.sendAdminNoticeDMs(admins, nextSurvey)
}

func (p *Plugin) sendAdminNoticeEmails(admins []*model.User) {
	config := p.API.GetConfig()

	subject := fmt.Sprintf(adminEmailSubject, *config.TeamSettings.SiteName, DAYS_UNTIL_SURVEY)

	bodyProps := map[string]interface{}{
		"SiteURL":         *config.ServiceSettings.SiteURL,
		"DaysUntilSurvey": DAYS_UNTIL_SURVEY,
	}
	if config.EmailSettings.FeedbackOrganization != nil && *config.EmailSettings.FeedbackOrganization != "" {
		bodyProps["Organization"] = "Sent by " + *config.EmailSettings.FeedbackOrganization
	} else {
		bodyProps["Organization"] = ""
	}

	var buf bytes.Buffer
	if err := adminEmailBodyTemplate.Execute(&buf, bodyProps); err != nil {
		p.API.LogError("Failed to prepare NPS survey notification email", "err", err)
		return
	}
	body := buf.String()

	for _, admin := range admins {
		p.API.LogDebug("Sending NPS survey notification email", "email", admin.Email)

		if err := p.API.SendMail(admin.Email, subject, body); err != nil {
			p.API.LogError("Failed to send NPS survey notification email", "email", admin.Email, "err", err)
		}
	}
}

func (p *Plugin) sendAdminNoticeDMs(admins []*model.User, nextSurvey time.Time) {
	// Actual DMs will be sent when the admins next log in, so just mark that they're scheduled to receive one
	for _, admin := range admins {
		noticeBytes, err := json.Marshal(&adminNotice{
			Sent:       false,
			NextSurvey: nextSurvey,
		})
		if err != nil {
			p.API.LogError("Failed to encode scheduled admin notice", "err", err.Error())
			continue
		}

		if appErr := p.API.KVSet(ADMIN_DM_NOTICE_KEY+admin.Id, noticeBytes); appErr != nil {
			p.API.LogError("Failed to store scheduled admin notice", "err", err.Error())
			continue
		}
	}
}

func (p *Plugin) getAdminUsers(perPage int) ([]*model.User, *model.AppError) {
	var admins []*model.User

	page := 0

	for {
		adminsPage, err := p.API.GetUsers(&model.UserGetOptions{Page: page, PerPage: perPage, Role: "system_admin"})
		if err != nil {
			return nil, err
		}

		for _, admin := range adminsPage {
			// Filter out deactivated users
			if admin.DeleteAt > 0 {
				continue
			}

			admins = append(admins, admin)
		}

		if len(adminsPage) < perPage {
			break
		}

		page += 1
	}

	return admins, nil
}

func (p *Plugin) checkForAdminNoticeDM(user *model.User) *adminNotice {
	if !isSystemAdmin(user) {
		return nil
	}

	noticeBytes, appErr := p.API.KVGet(ADMIN_DM_NOTICE_KEY + user.Id)
	if appErr != nil {
		p.API.LogError("Failed to get scheduled admin notice", "err", appErr)
		return nil
	}

	if noticeBytes == nil {
		// No notice stored for this user, likely because they were created after the survey was scheduled
		return nil
	}

	var notice adminNotice

	if err := json.Unmarshal(noticeBytes, &notice); err != nil {
		p.API.LogError("Failed to decode scheduled admin notice", "err", err)
		return nil
	}

	if notice.Sent {
		// Already sent
		return nil
	}

	return &notice
}

func isSystemAdmin(user *model.User) bool {
	for _, role := range strings.Fields(user.Roles) {
		if role == model.SYSTEM_ADMIN_ROLE_ID {
			return true
		}
	}

	return false
}

func (p *Plugin) sendAdminNoticeDM(user *model.User, notice *adminNotice) {
	p.API.LogDebug("Sending admin notice DM", "user_id", user.Id)

	// Send the DM
	body := fmt.Sprintf(adminDMBody, notice.NextSurvey.Format("January 2, 2006"))

	if appErr := p.CreateBotDMPost(user.Id, body, "custom_nps_admin_notice"); appErr != nil {
		p.API.LogError("Failed to send admin notice", "err", appErr)
		return
	}

	// Store that the DM has been sent
	notice.Sent = true

	noticeBytes, err := json.Marshal(notice)
	if err != nil {
		p.API.LogError("Failed to encode sent admin notice. Admin notice will be resent on next refresh.", "err", err.Error())
		return
	}

	if appErr := p.API.KVSet(ADMIN_DM_NOTICE_KEY+user.Id, noticeBytes); appErr != nil {
		p.API.LogError("Failed to save sent admin notice. Admin notice will be resent on next refresh.", "err", appErr)
	}
}

func (p *Plugin) shouldSendSurveyDM(user *model.User, now time.Time) bool {
	// TODO
	return false
}

func (p *Plugin) sendSurveyDM(user *model.User) {
	// TODO
}
