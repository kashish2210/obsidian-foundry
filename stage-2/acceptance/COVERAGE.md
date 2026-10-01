# Stage 2 acceptance coverage (ledger R1-R197)

Commit this file with `git add -f` (the root `coverage.*` ignore rule matches it).

## How to run

- API suite (Go, stdlib only, sequential, ~75 s):
  `cd acceptance && BASE_URL=http://localhost:8080 go test -count=1 ./...`
  On this Windows host set `GOTMPDIR=K:/hackathons/band-work/gotmp` if Application Control blocks the test exe.
- Browser suite (Python Playwright; Chromium, else Chrome/Edge via `PW_CHANNEL`):
  `cd acceptance && BASE_URL=http://localhost:8080 conda run -n venv python -m pytest ui`
- Every test resets the service itself. Tests are named by ledger id (`TestR74_...`, `test_R142_...`).
- `testdata/stage1_export.json` + `stage1_meta.json` are a real export from the frozen stage-1 service (R160).

## Ids not testable black-box

| id | why |
|---|---|
| R5, R6 | Dockerfile/RUN.md deliverable and "no outbound network at run time"; needs the image/container (reviewer gates 1-2). R126 checks only that the browser makes no cross-origin requests |
| R7, R9 | 2 vCPU / 2 GiB / 60 s start limits and restart persistence are deployment properties (R8 latency is checked) |
| R10 | default port 8080 when PORT is unset; the suite gets one address from BASE_URL (reviewer gate 2) |
| R21 | "handle never changes": no endpoint can change a handle; uniqueness/format are covered by R23/R57 |
| R38 | administrative balance endpoint is out of scope |
| R60 | password hashing is not observable over HTTP (login exactness is R52/R56) |
| R127, R128 (look), R129 (look), R131 (contrast) | visual judgement: **reviewer inspection**. Look at: calm, consistent finance look (one type scale, spacing, colour, control styles; obvious primary actions); available funds the largest money value with total and held visibly secondary; pending/loading/success/refused/uncertain states visibly different (not only text); in/out direction, privacy, status readable without raw API data; people, amounts, timestamps formatted for humans; designed empty/loading/error states; text contrast >= 4.5:1. Measurable parts are tested (R128 font-size order, R129 no raw ids/JSON/RFC 3339 in feed rows, R131 labels/focus/navigation) |
| R125 | "found by exact data-testid" is exercised by every UI test rather than by one test |
| R196 | race of reset/import with an in-flight write or login is not reproducible from outside without control of timing; covered only indirectly by TestR163_R166_ConcurrentMixedInvariants and the import tests (reviewer code review: token re-resolved inside the write lock) |
| R197 | fixture password-hash errors cannot be provoked over HTTP (reviewer code review) |

## Readings chosen where the spec is ambiguous (stage 2)

- A capture on a clock-expired authorization may report `authorization_expired` or `authorization_not_open` (both 409); all other closed states must be `authorization_not_open`.
- Error precedence for capture (R183): unknown -> 404; caller not the receiver -> 403 (even on closed/expired); then state (409, even for an oversized or invalid amount on a closed authorization); then amount (422 validation before exceed on an open one).
- `payment_ids` is an empty array (not null) on a fresh authorization.
- `final` of any non-boolean (string, number, array, object) -> 400 `malformed_request`; `final: null` is not tested.
- `authorization_ttl_seconds` of `0`, negative, fractional, boolean and a **string** (`"600"`) -> 422; `null` is not tested.
- UI: the session survives full navigations (same browser context); logging in again replaces it. `wallet-refresh` must stay clickable while an earlier refresh is in flight (R154).
- UI split/pay inputs compare by value, never by whitespace-normalised text; note text is compared with `textContent`.

## Stage 1 ids (R1-R122) -> test names (API suite, unchanged from accepted 0302166 plus stage-2 aware helpers)

R1 TestR1_R2_R50_ConcurrentPaymentsInvariants, TestR1_R50_MixedLoad | R2 TestR1_R2_R50..., TestR2_ConflictingSpendsOfOneWallet, TestR2_OppositeDirectionsNoDeadlock | R3 TestR3_ConcurrentPayOfOneRequestDistinctKeys, TestR3_PayVsDeclineVsCancelRace, TestR3_R2_ManyRequestsOnePayerLimitedFunds | R4 TestR4_MoneyOnlyBetweenExistingWallets |
R8 TestR8_FiftyInFlightWithinDeadline | R11 TestR11_Health | R12 TestR12_ResetReplacesAllState | R13 TestR13_* | R14 TestR14_JSONContentType | R15 TestR15_TimestampsRFC3339WithOffset | R16 TestR16_UnknownBodyFieldsIgnored | R17 TestR17_UnknownQueryParamsIgnored | R18 TestR18_IdsAreStringsUpTo64 | R19 TestR19_CurrencyAndMinorUnits | R20 TestR20_AmountParsing, TestR20_AmountBoundaries | R22 TestR22_R34_R35_R36_SeededFixture | R23 TestR23_DerivedHandle | R24 TestR24_NewUserStartsAtZeroAndReceives | R25 TestR25_PaymentVisibleImmediatelyAndBalancesConsistent | R26 TestR26_RequestLifecycleTerminalStates | R27 TestR83_R27_RequestMayExceedPayerBalance | R28 TestR28_R30_RequestsNeverInFeedVisibilityFromPayer | R29 TestR29_R32_FeedVisibilityRule | R30 TestR30_R91_ListScopeOrderAndShape | R31 TestR31_SplitRequestsVisibleToTheirTwoPartiesOnly | R32 TestR29_R32_FeedVisibilityRule | R33 TestR33_LargeBalancesExact | R34 R35 R36 TestR22_R34_R35_R36_SeededFixture | R37 TestR37_NegativeFixtureBalanceRejected | R39 TestR39_ErrorBodyShape | R40 TestR40_* | R41 TestR41_MissingIdempotencyKeyAllPaths | R42 TestR42_Unauthenticated | R43 TestR43_GenericErrorCodes | R44 TestR44_InvalidValueOfCorrectTypeIs422 | R45 TestR45_NoteAndVisibilityTypes | R46 TestR94_R46_R49_RequestsPagination, TestR100_R49_R46_ActivityParamValidation | R47 TestR47_UnparseableBodies | R48 TestR48_KeyLength | R49 same as R46 | R50 TestR1_R2_R50... |
R51 TestR51_Signup | R52 TestR52_Login | R53 TestR53_EmailTaken | R54 TestR54_ShortPassword | R55 TestR55_BadEmail | R56 TestR56_LoginFailures | R57 TestR57_HandleTaken | R58 TestR58_AuthRequired | R59 TestR59_MultipleTokens |
R61 TestR61_MissingIdempotencyKey | R62/R63 TestR62_R63_FirstUseThenReplay | R64 TestR64_SameKeyDifferentBody | R65 TestR65_* | R66 TestR66_KeysScopedPerUser | R67 TestR67_SameKeyDifferentPathIsNotAReplay | R68 TestR68_SameBodyMeansSameJSONValue | R69 TestR69_* | R70 TestR70_ReplayReturnsOriginalAfterChange | R71 TestR71_ClaimedKeyResolvedBeforeValidation |
R72 TestR72_Me | R73 TestR73_PaymentShapeAndDefaults | R74 TestR74_InsufficientFunds | R75 TestR75_PaymentAmountValidation | R76 TestR76_SelfPayment | R77 TestR77_NoteLength | R78 TestR78_PaymentRecipientLookup | R79 TestR79_FailedPaymentLeavesNoTrace | R80 TestR80_NoteVerbatim | R81 TestR81_RequestShape | R82 TestR82_RequestValidation | R83 TestR83_R27_... | R84 TestR84_PayRequest | R85 TestR85_PayErrors | R86 TestR86_NonPartiesAre403NotFound404 | R87 TestR87_PayBodyEmptyVsExplicitPublic | R88 TestR88_PayReplayAfterPaid | R89 TestR89_Decline | R90 TestR90_Cancel | R91 TestR30_R91_... | R92 TestR92_DirectionFilter | R93 TestR93_StatusFilter | R94 TestR94_R46_R49_RequestsPagination, TestR94_DefaultLimit50 | R95 R96 R97 TestR95_R96_R97_SplitShapeAndRequests, TestR96_* | R98 TestR98_SplitValidation | R99 TestR99_OnlyCallerParticipant | R100 TestR100_* |
R101 TestR101_SplitTable | R102 TestR102_* | R103 TestR103_SplitsIndependentAndSumPreserved |
R104 TestR104_ExportShape | R105 R106 R109 TestR105_R106_R109_ImportRestoresEverything, TestR109_* | R107 TestR107_InvalidImports | R108 TestR108_ExportIsASnapshot | R110 TestR110_R70_RetriesAndReceiptsSurviveImport | R111 TestR111_NewIdsDoNotCollideAfterImport, TestR111_ResetClearsImportedState |
R112 TestR112_MultipleOperatorsAndOperatorIsNotSuperuser | R113 TestR115_R119_SettlementShape, TestR73 | R114 TestR114_SettlementPermissions | R115 TestR115_NoteVisibilityAndBounds | R116 TestR116_MalformedBatchShapes, TestR116_EntryErrorsInInputOrderBeforeFunds | R117 TestR117_NettedAffordability | R118 TestR118_AllOrNothingAndFailedClaimsNoKey | R119 TestR119_SettlementIdsUniqueAcrossBatches, TestR115_R119_... | R120 TestR120_ConstituentVisibility | R121 TestR121_SettlementReplay | R122 TestR122_OperatorAndSettlementsPreserved

## Stage 2 ids (R123-R197) -> tests

| id | test(s) |
|---|---|
| R123 | ui/test_auth.py::test_R123_routes_reachable_and_navigable |
| R124 | ui/test_layout_ui.py::test_R124_content_negotiation, test_R124_static_routes_serve_html |
| R125 | every ui test (exact data-testid lookups) |
| R126 | ui/test_layout_ui.py::test_R126_no_external_requests |
| R127 | reviewer inspection (see above) |
| R128 | ui/test_authz_ui.py::test_R189_wallet_numbers (font-size order); look is reviewer inspection |
| R129 | ui/test_layout_ui.py::test_R129_human_readable_states (measurable parts); rest reviewer inspection |
| R130 | ui/test_layout_ui.py::test_R130_no_horizontal_scroll[375/768/1280], test_R130_form_error_states_do_not_overflow_at_375 |
| R131 | ui/test_layout_ui.py::test_R131_inputs_have_visible_labels, test_R131_keyboard_focus_is_visible, test_R131_consistent_navigation; contrast and empty/loading design are reviewer inspection |
| R132 | ui/test_auth.py::test_R132_R134_signup_signs_in, test_R132_R133_login_and_auth_error |
| R133 | ui/test_auth.py::test_R132_R133_login_and_auth_error, test_R133_signup_errors |
| R134 | ui/test_auth.py::test_R132_R134_signup_signs_in, test_R134_R136_current_user_on_every_screen_and_session_persists |
| R135 | ui/test_auth.py::test_R135_logout |
| R136 | ui/test_auth.py::test_R134_R136_current_user_on_every_screen_and_session_persists |
| R137, R138 | ui/test_money_ui.py::test_R137_R138_balance_format[10 cases], test_R138_format_everywhere[EUR/JPY/BHD] |
| R139 | ui/test_money_ui.py::test_R140_*, test_R141_*, test_R142_* (form elements) |
| R140 | ui/test_money_ui.py::test_R140_decimal_conversion_exact, test_R140_units_zero_and_three, test_R140_rejected_inputs_send_nothing |
| R141 | ui/test_money_ui.py::test_R141_pay_error_on_refusal_and_absent_otherwise |
| R142 | ui/test_money_ui.py::test_R142_unchanged_resubmit_pays_once |
| R143 | ui/test_money_ui.py::test_R143_request_form |
| R144 | ui/test_money_ui.py::test_R144_R145_feed_items, test_R144_feed_visibility_rule_in_ui |
| R145 | ui/test_money_ui.py::test_R144_R145_feed_items |
| R146 | ui/test_money_ui.py::test_R146_empty_activity |
| R147 | ui/test_requests_split_ui.py::test_R147_R148_lists_statuses_and_buttons |
| R148 | ui/test_requests_split_ui.py::test_R147_R148_lists_statuses_and_buttons |
| R149 | ui/test_requests_split_ui.py::test_R149_empty_requests, test_R149_request_error_when_payer_is_short, test_R147_R149_pay_decline_cancel_actions |
| R150 | ui/test_requests_split_ui.py::test_R150_split_errors_send_nothing_or_show_error, test_R150_split_only_caller_creates_no_requests |
| R151 | ui/test_requests_split_ui.py::test_R151_split_preview_matches_rule_and_server[5 cases], test_R151_split_preview_updates_and_formats |
| R152 | ui/test_money_ui.py::test_R152_R159_state_refreshes_after_actions, test_R147_R149_pay_decline_cancel_actions |
| R153 | ui/test_money_ui.py::test_R153_wallet_refresh_keeps_pay_form |
| R154 | ui/test_money_ui.py::test_R154_latest_refresh_wins, test_R154_out_of_order_three_refreshes |
| R155 | ui/test_money_ui.py::test_R155_refused_payment_refreshes_and_keeps_inputs |
| R156 | ui/test_requests_split_ui.py::test_R156_request_cancelled_elsewhere, test_R156_decline_and_cancel_refused_elsewhere |
| R157, R158 | ui/test_money_ui.py::test_R157_R158_lost_response_then_retry_once[commit/no-commit], test_R157_changed_form_after_uncertain_is_a_new_payment |
| R159 | ui/test_money_ui.py::test_R152_R159_state_refreshes_after_actions, ui/test_authz_ui.py::test_R189_held_absent_when_zero_and_appears_without_reload |
| R160 | TestR160_R161_Stage1ExportImports (real stage-1 export in testdata/) |
| R161 | TestR160_R161_Stage1ExportImports; ui/test_money_ui.py::test_R161_signed_in_browser_survives_import_and_pays_pending_request (browser case uses a stage-2 export: a stage-1 session token cannot be injected into the browser without knowing the UI's storage) |
| R162 | ui/test_money_ui.py::test_R162_lost_payment_recovered_after_import_without_reload; API part in TestR160_R161_Stage1ExportImports (replay of the stage-1 lost payment) |
| R163 | TestR163_HoldMovesNoMoney, TestR163_R166_ConcurrentMixedInvariants |
| R164 | TestR164_* (held funds cannot fund payments/authorizations/request pays/settlements; captures spend held money), TestR172_AvailableGovernsInsufficientFunds |
| R165 | TestR165_ConcurrentPartialCapturesNeverExceed, TestR164_R165_ConcurrentFinalCapturesDistinctKeys, TestR181/R182 |
| R166 | TestR163_R166_ConcurrentMixedInvariants, TestR164_PaymentsAndCapturesAgainstHeldFunds, TestR164_VoidVersusCaptureRace |
| R167 | TestR167_TTL (known finding: string ttl accepted by the service) |
| R168 | TestR168_R169_R170_SeededAuthorizations |
| R169 | TestR169_SeededHoldsOverBalance |
| R170 | TestR170_LazyExpiry, TestR170_ExpiryOnWriteWithoutPriorRead, TestR168_R169_R170_SeededAuthorizations |
| R171 | TestR171_ExpiryOfPartiallyCapturedKeepsRecords |
| R172 | TestR172_AvailableGovernsInsufficientFunds |
| R173 | TestR173_MeFields |
| R174 | TestR174_PaymentsLeaveNoHold |
| R175 | TestR175_AuthorizationIdOnPayments |
| R176 | TestR176_NewPathsIdempotency, TestR176_SameKeyOnDifferentNewPaths, TestR176_R70_ReplayAfterChange, TestR176_ConcurrentIdenticalAuthorizeAndCapture |
| R177 | TestR177_AuthorizationShape |
| R178 | TestR178_AuthorizeValidation |
| R179 | TestR179_AuthorizationNeverInFeed |
| R180 | TestR180_CaptureBodyAndPermissions |
| R181 | TestR181_FinalCaptureReleasesRemainder |
| R182 | TestR182_PartialCaptures |
| R183 | TestR183_CaptureErrorPrecedence |
| R184 | TestR184_CaptureReplayBodies |
| R185 | TestR185_Void |
| R186 | TestR186_VoidReleasesOnlyRemainder |
| R187 | TestR187_R188_ListScopeOrderAndFilters |
| R188 | TestR187_R188_ListScopeOrderAndFilters, TestR188_Pagination |
| R189 | ui/test_authz_ui.py::test_R189_wallet_numbers, test_R189_held_absent_when_zero_and_appears_without_reload |
| R190 | ui/test_authz_ui.py::test_R190_authorize_form, test_R190_authorize_insufficient_counts_held_funds |
| R191 | ui/test_authz_ui.py::test_R191_authorization_list, test_R191_order_newest_first |
| R192 | ui/test_authz_ui.py::test_R192_buttons_only_where_allowed, test_R192_capture_prefill_follows_remaining |
| R193 | ui/test_authz_ui.py::test_R193_capture_flow_and_errors, test_R193_capture_refused_after_void_elsewhere, test_R193_void_flow, test_R193_empty_authorizations |
| R194 | ui/test_authz_ui.py::test_R194_seeded_holds_shown_immediately |
| R195 | TestR195_ExportImportAuthorizations, TestR195_ClockExpiryAcrossImport |
| R196 | not testable (see above); reviewer code review |
| R197 | not testable (see above); reviewer code review |

Stage-1 nits fixed in this copy of COVERAGE.md: R113 is asserted in TestR115_R119_SettlementShape (no dedicated test); in TestR44 the `x@nodomain` 422 comes from the short password.
