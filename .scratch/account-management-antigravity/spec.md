# Account management and Antigravity quota

Implement distinct add, reconnect, replace and remove actions. A replacement retains the selected old credential until a verified new login succeeds and the user confirms the identity change. Failed, ambiguous or mismatched logins must not remove it. Removal keeps recorded history, clears local quota and pause state, and affects only the selected credential.

Fetch Antigravity quota across partial endpoint responses, preserve group and bucket IDs, and fetch the plan independently. Show every reported quota group with separate 5-hour and weekly limits. Missing Gemini quota is explicit. The user selected option A with all groups visible, within the existing dashboard style.

Preserve history, sync configuration and unrelated credentials. Sync continues to exchange observations only. Do not add Antigravity history or new service-management commands.

Verify backend behavior with fixture CPA/provider responses, run Go and existing JS checks, and inspect the affected UI using isolated data. Create an implementation PR and stop before merge. User explicitly authorized updating the installed app. Preserve service arguments and maintain a rollback binary; verify the installed service and quota groups after restart.
