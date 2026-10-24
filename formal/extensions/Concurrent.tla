---------------------------- MODULE Concurrent ----------------------------
EXTENDS Naturals
CONSTANT Serialized
\* One activity and its matching metadata, each delivered once. Individual
\* shared accesses are idealized as atomic; this is NOT Go data-race semantics.
VARIABLES metadata, pending, result, aPC, mPC, lookup, work, lock
vars == <<metadata, pending, result, aPC, mPC, lookup, work, lock>>
Init ==
    /\ metadata = FALSE /\ pending = FALSE /\ result = "absent"
    /\ aPC = 0 /\ mPC = 0 /\ lookup = FALSE /\ work = FALSE /\ lock = "free"

ActivityLookup ==
    /\ aPC = 0 /\ (~Serialized \/ lock = "free")
    /\ aPC' = 1 /\ lookup' = metadata
    /\ lock' = IF Serialized THEN "activity" ELSE lock
    /\ UNCHANGED <<metadata, pending, result, mPC, work>>
ActivityRegister ==
    /\ aPC = 1 /\ aPC' = 2
    /\ pending' = IF lookup THEN pending ELSE TRUE
    /\ UNCHANGED <<metadata, result, mPC, lookup, work, lock>>
ActivityReturn ==
    /\ aPC = 2 /\ aPC' = 3
    /\ result' = IF lookup THEN "resolved" ELSE "unresolved"
    /\ lock' = IF Serialized THEN "free" ELSE lock
    /\ UNCHANGED <<metadata, pending, mPC, lookup, work>>

MetadataInsert ==
    /\ mPC = 0 /\ (~Serialized \/ lock = "free")
    /\ mPC' = 1 /\ metadata' = TRUE
    /\ lock' = IF Serialized THEN "metadata" ELSE lock
    /\ UNCHANGED <<pending, result, aPC, lookup, work>>
MetadataScan ==
    /\ mPC = 1 /\ mPC' = 2 /\ work' = pending
    /\ UNCHANGED <<metadata, pending, result, aPC, lookup, lock>>
MetadataReturn ==
    /\ mPC = 2 /\ mPC' = 3
    /\ result' = IF work THEN "resolved" ELSE result
    /\ pending' = FALSE
    /\ lock' = IF Serialized THEN "free" ELSE lock
    /\ UNCHANGED <<metadata, aPC, lookup, work>>

Next == ActivityLookup \/ ActivityRegister \/ ActivityReturn
        \/ MetadataInsert \/ MetadataScan \/ MetadataReturn
Spec == Init /\ [][Next]_vars
TypeOK ==
    /\ metadata \in BOOLEAN /\ pending \in BOOLEAN
    /\ result \in {"absent", "unresolved", "resolved"}
    /\ aPC \in 0..3 /\ mPC \in 0..3
    /\ lookup \in BOOLEAN /\ work \in BOOLEAN
    /\ lock \in {"free", "activity", "metadata"}
LockDiscipline == Serialized =>
    /\ (lock = "activity") = (aPC \in 1..2)
    /\ (lock = "metadata") = (mPC \in 1..2)
QuiescentUseful == (aPC = 3 /\ mPC = 3) => result = "resolved"
=============================================================================
