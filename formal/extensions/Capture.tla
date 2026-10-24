------------------------------ MODULE Capture ------------------------------
EXTENDS Naturals
CONSTANTS Mode, MaxBoots, MaxIncarnations, None
Keys == [scope : 1..MaxBoots, pid : {7}, incarnation : 1..MaxIncarnations]

VARIABLES boot, detectedBoot, incarnation, live, phase, oracle, snapshot, reported
vars == <<boot, detectedBoot, incarnation, live, phase, oracle, snapshot, reported>>
PhysicalKey == [scope |-> boot, pid |-> 7, incarnation |-> incarnation]
Init ==
    /\ boot = 1 /\ detectedBoot = 1 /\ incarnation = 0 /\ live = FALSE
    /\ phase = "idle" /\ oracle = None /\ snapshot = None /\ reported = None

Start ==
    /\ ~live /\ incarnation < MaxIncarnations
    /\ incarnation' = incarnation + 1 /\ live' = TRUE
    /\ UNCHANGED <<boot, detectedBoot, phase, oracle, snapshot, reported>>
Exit ==
    /\ live /\ live' = FALSE
    /\ UNCHANGED <<boot, detectedBoot, incarnation, phase, oracle, snapshot, reported>>
\* A physical reboot kills the current process and resets the allocator.
\* Old captured data survives in transit; boot detection is a separate action.
Reboot ==
    /\ boot < MaxBoots
    /\ boot' = boot + 1 /\ incarnation' = 0 /\ live' = FALSE
    /\ UNCHANGED <<detectedBoot, phase, oracle, snapshot, reported>>
DetectBoot ==
    /\ detectedBoot # boot /\ detectedBoot' = boot
    /\ UNCHANGED <<boot, incarnation, live, phase, oracle, snapshot, reported>>

\* The private oracle records the physical lifetime when the event occurs.
\* Sensor output never reads that ledger; snapshot is the proposed coherent
\* capture operation. Its implementability on a real sensor remains unverified.
BeginCapture ==
    /\ live /\ phase = "idle"
    /\ phase' = "observing"
    /\ oracle' = PhysicalKey
    /\ snapshot' = CASE Mode = "late" -> None
                       [] Mode = "cached-boot" ->
                           [scope |-> detectedBoot, pid |-> 7, incarnation |-> incarnation]
                       [] OTHER -> PhysicalKey
    /\ UNCHANGED <<boot, detectedBoot, incarnation, live, reported>>
FinishCapture ==
    /\ phase = "observing"
    /\ Mode # "late" \/ live
    /\ phase' = "done"
    /\ reported' = IF Mode = "late" THEN PhysicalKey ELSE snapshot
    /\ UNCHANGED <<boot, detectedBoot, incarnation, live, oracle, snapshot>>

Next == Start \/ Exit \/ Reboot \/ DetectBoot \/ BeginCapture \/ FinishCapture
Spec == Init /\ [][Next]_vars
TypeOK ==
    /\ boot \in 1..MaxBoots /\ detectedBoot \in 1..boot
    /\ incarnation \in 0..MaxIncarnations /\ live \in BOOLEAN
    /\ live => incarnation > 0
    /\ phase \in {"idle", "observing", "done"}
    /\ oracle \in Keys \cup {None}
    /\ snapshot \in Keys \cup {None}
    /\ reported \in Keys \cup {None}
CaptureIdentity == reported # None => reported = oracle
=============================================================================
