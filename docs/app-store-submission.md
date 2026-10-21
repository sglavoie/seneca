# App Store submission

**October 21, 2026.** What Apple requires before the iPhone app can be accepted,
what is done, and what is left. Checked that day against the App Review
Guidelines, the App Privacy details page and Apple's upcoming requirements;
re-read the [sources](#sources) before submitting.

## The website

Apple requires two public pages that are live, with final content, at
submission: a privacy policy (guideline 5.1.1(i)) and a support page with real
contact information (guideline 1.5). A marketing page is optional.

All three are in [`website/`](../website), a static site with no scripts,
cookies, analytics or third-party requests:

| Page | URL | App Store Connect field |
| --- | --- | --- |
| Home | `https://seneca.sglavoie.com/` | Marketing URL |
| Privacy policy | `https://seneca.sglavoie.com/privacy/` | Privacy Policy URL |
| Support | `https://seneca.sglavoie.com/support/` | Support URL |

The privacy policy is written from the code as it stands; see
[What the policy rests on](#what-the-policy-rests-on). Update it, and its
effective date, before any change that moves data off the iPhone ships.

### Publishing it

The site is deployed by direct upload to Cloudflare Pages, from the
`website/` directory alone.

1. `just website-preview` serves it at <http://localhost:8765>.
2. `npx --yes wrangler@4 login` once, then `just website-deploy`. The first
   deploy offers to create the `seneca` Pages project.
3. In the Cloudflare dashboard: Workers & Pages › seneca › Custom domains › add
   `seneca.sglavoie.com`. The `sglavoie.com` zone is already on Cloudflare, so
   the DNS record is created for you.
4. Email › Email Routing on `sglavoie.com`: route `seneca@sglavoie.com` to your
   inbox, and send it a test message. The support page promises an answer.

`website/_headers` sets a strict Content Security Policy; a page that needs a
script or an outside resource must change it.

## Done in the app

- **Privacy and support links** in Settings (guidelines 5.1.1(i) and 1.5), shown
  even when the journal cannot be read.
- **Privacy manifest**: `ios/SenecaMobile/PrivacyInfo.xcprivacy`, no collected
  data, no tracking, reasons for the file-timestamp, disk-space and
  user-defaults APIs. `ios/scripts/testflight.sh` refuses an archive without it.
- **Export compliance**: `ITSAppUsesNonExemptEncryption` is `NO` in
  `ios/project.yml`.
- **Microphone purpose string**, and no other permission is requested.
- **Toolchain**: iOS 26 SDK and deployment target.

## App Store Connect answers

- **App Privacy: Data Not Collected.** Nothing leaves the iPhone in a way the
  developer can access. Sync goes to a folder the user picks, which Apple's
  definitions do not count as collection by the developer.
- **Age rating**: every content question is "None" or "No". There is no web
  view, no user-to-user communication, and no content shared with other users.
  Answer the health and wellness question carefully: the Step 10 pack comes
  from twelve-step recovery, but the app gives no medical or treatment
  information. The rating should come out at 4+.
- **Category**: Lifestyle, matching `LSApplicationCategoryType`. Health &
  Fitness invites scrutiny that `docs/product/scope.md` already rules out: the
  app is not clinical or mental-health.
- **Copyright**: `2026 Sébastien Lavoie`.
- **Price**: free. There is no StoreKit code.
- **Accounts**: none, so no demo account and no account deletion
  (5.1.1(v)).

## Left to do

1. Publish the website (above) and check that all three URLs load over HTTPS.
2. Create the app record for `io.sglavoie.seneca.mobile` in App Store Connect.
   The name "Seneca" may already be taken; have a fallback such as "Seneca:
   Evening Review".
3. Decide what the Mac references mean for a public user (see
   [Review risks](#review-risks)).
4. EU distribution: declare trader or non-trader status under the Digital
   Services Act. As a trader, the address, phone and email you give Apple are
   shown on the EU product page.
5. Screenshots for the 6.9-inch iPhone size, showing the app in use (2.3.3):
   Today with an intention, the writing form, History, Follow-Through, and
   Practice.
6. Description, subtitle, keywords and promotional text. Avoid clinical or
   therapeutic claims.
7. App Review contact (name, email, phone) and the review notes below.
8. Upload with `just ios-testflight --upload`, test through TestFlight, submit.

### Review notes (draft)

> Seneca is a private evening journal. There is no account and no server; it
> collects no data. To try it: on first launch choose Start a Journal, pick a
> practice, and skip reminders and sync. On Today, tap Write Today's
> Reflection to answer the evening's questions, then save. The answer to the last question appears the
> next day as the intention.
>
> Voice: Speak Today's Reflection records the answers aloud; transcription
> runs on the device. Splitting one recording into an answer per question uses Apple's
> on-device model and appears only on devices with Apple Intelligence.
>
> Recording continues when the screen locks, which is why the app declares the
> audio background mode. Sync is optional and writes to a folder the user picks
> in iCloud Drive; nothing is sent to us.

## Review risks

- **The Mac companion.** Guideline 4.2.3(i): "Your app should work on its own
  without requiring installation of another app to function." The iPhone app
  does: writing, voice, History, the focus, Follow-Through, practice packs and
  sharing need nothing else. Reviews are the exception: the Reviews tab says
  "Reviews are written with Seneca on your Mac and appear here", and Sync and
  Reminders also speak of "your Mac". A reviewer cannot reach that software,
  and 2.3.1(a) wants every feature "accessible for review", so an empty tab
  pointing at it invites a 2.1 rejection. Publishing the Mac tool (outside the
  Mac App Store, Apple does not review it) makes the reference honest but does
  not make it reviewable: reviews also need a Claude, Copilot or OpenAI
  account. The safer first release shows Reviews only once the journal holds
  one, and words Sync and Reminders for an iPhone-only user.
- **Third-party AI, if the Mac tool is published.** Desktop reviews send
  entries to the AI provider the user configures. Guideline 5.1.2(i) requires
  clear disclosure of sharing "with third-party AI". The iPhone app sends
  nothing, but once the website or the app points to the Mac tool, the privacy
  policy must say what that tool sends and to whom.
- **Apple Intelligence.** A review device may not support it; answer splitting
  must be absent there, not broken. `LanguageModelSupport` already gates it.
- **Background audio.** Explained in the review notes.
- **Minimum functionality (4.2).** Journaling apps are common; the description
  and screenshots should show what is particular to this one: the ordered
  questions, the intention carried forward, Follow-Through, and the practice
  packs.

## What the policy rests on

Checked in the code on October 21, 2026:

- No network code: no `URLSession`, no third-party packages (`ios/project.yml`
  lists only local packages), and the Go engine links no `net` package.
  `mobile/senecabridge/provider_boundary_test.go` fails if a cloud model SDK is
  linked into the phone build.
- Speech uses `SpeechAnalyzer` on the device; iOS may download speech models.
  Answer splitting uses `SystemLanguageModel` on the device. Private Cloud
  Compute appears only in the debug-only Model Evaluation screen, which
  `testflight.sh` keeps out of release archives.
- Sync is a user-picked folder through a security-scoped bookmark. There is no
  CloudKit and no iCloud entitlement. Content in the folder is not encrypted by
  the app, and earlier versions of entries stay in it
  ([sync-format.md](mobile/sync-format.md)).
- Notifications are local, with fixed text.
- Sharing goes only through the share sheet; diagnostics contain no writing.

If any of these stops being true, the privacy policy, the App Privacy answers
and the privacy manifest all change with it.

## Sources

- [App Review Guidelines](https://developer.apple.com/app-store/review/guidelines/)
- [Platform version information](https://developer.apple.com/help/app-store-connect/reference/app-information/platform-version-information)
- [Manage app privacy](https://developer.apple.com/help/app-store-connect/manage-app-information/manage-app-privacy)
- [Upcoming requirements](https://developer.apple.com/news/upcoming-requirements/)
- [App privacy details](https://developer.apple.com/app-store/app-privacy-details/)
  (definition of "collect"; on-device processing is not collection)
- [Describing use of required reason API](https://developer.apple.com/documentation/bundleresources/privacy_manifest_files/describing_use_of_required_reason_api)
- [EU Digital Services Act trader requirements](https://developer.apple.com/help/app-store-connect/manage-compliance-information/manage-european-union-digital-services-act-trader-requirements/)
