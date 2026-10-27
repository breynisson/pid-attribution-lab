----------------------------- MODULE Retention -----------------------------
EXTENDS Naturals, FiniteSets
CONSTANTS Fault, MaxTime, MetadataTTL, PendingTTL, MaxMetadata, MaxEvents, None
Keys == {"A", "B"}
Events == {"E1", "E2"}
\* Fixed valid physical prefix: A starts, captures E1/E2, exits; B reuses its
\* PID. Keys denote complete distinct identities. Both events belong to A.
Truth == [e \in Events |-> "A"]
VARIABLES now, metadata, received, admitted, results, dead, published, up
vars == <<now, metadata, received, admitted, results, dead, published, up>>
StoredKeys == {k \in Keys : metadata[k] # None}
StoredEvents == {e \in Events : received[e] # None}
Due(at, ttl) == ttl # 0 /\ now - at >= ttl
Answer(k) ==
    IF Fault = "always-unresolved" THEN None
    ELSE IF metadata[k] # None THEN k
    ELSE IF Fault = "pid-fallback" /\ StoredKeys # {}
         THEN CHOOSE other \in StoredKeys : TRUE ELSE None
Init ==
    /\ now = 0
    \* A's metadata was admitted before the transport suffix begins.
    /\ metadata = [k \in Keys |-> IF k = "A" THEN 0 ELSE None]
    /\ received = [e \in Events |-> None]
    /\ admitted = [e \in Events |-> 0]
    /\ results = [e \in Events |-> None]
    /\ dead = {}
    /\ published = [e \in Events |-> None]
    /\ up = TRUE

Activity(e, k) ==
    /\ up /\ e \notin StoredEvents /\ Cardinality(StoredEvents) < MaxEvents
    /\ received' = [received EXCEPT ![e] = k]
    /\ admitted' = [admitted EXCEPT ![e] = now]
    /\ results' = [results EXCEPT ![e] = Answer(k)]
    /\ published' = [published EXCEPT ![e] = Answer(k)]
    /\ UNCHANGED <<now, metadata, dead, up>>
Meta(k) ==
    /\ up /\ k \notin StoredKeys /\ Cardinality(StoredKeys) < MaxMetadata
    /\ metadata' = [metadata EXCEPT ![k] = now]
    /\ LET matched == {e \in StoredEvents : received[e] = k /\ results[e] = None
                          /\ (e \notin dead \/ Fault = "revive")
                          /\ Fault # "always-unresolved"}
       IN /\ results' = [e \in Events |-> IF e \in matched THEN k ELSE results[e]]
          /\ published' = [e \in Events |-> IF e \in matched THEN k ELSE published[e]]
    /\ UNCHANGED <<now, received, admitted, dead, up>>
Tick ==
    /\ up /\ now < MaxTime
    /\ now' = now + 1
    /\ metadata' = [k \in Keys |->
          IF metadata[k] # None /\ MetadataTTL # 0
             THEN IF now + 1 - metadata[k] >= MetadataTTL THEN None ELSE metadata[k]
             ELSE metadata[k]]
    /\ dead' = dead \cup {e \in StoredEvents : results[e] = None /\ PendingTTL # 0
                                             /\ now + 1 - admitted[e] >= PendingTTL}
    /\ UNCHANGED <<received, admitted, results, published, up>>
Crash == /\ up /\ up' = FALSE
         /\ UNCHANGED <<now, metadata, received, admitted, results, dead, published>>
Restart ==
    /\ ~up /\ up' = TRUE
    /\ results' = IF Fault = "recompute" THEN
          [e \in Events |-> IF received[e] = None THEN None
                            ELSE IF metadata[received[e]] = None THEN None
                            ELSE results[e]] ELSE results
    /\ UNCHANGED <<now, metadata, received, admitted, dead, published>>
\* Replays and capacity rejections have no state effect and are covered by
\* stuttering. Environment supplies truthful capture keys, not resolver output.
Next == (\E e \in Events : Activity(e, Truth[e]))
        \/ (\E k \in Keys : Meta(k)) \/ Tick \/ Crash \/ Restart
Spec == Init /\ [][Next]_vars
TypeOK ==
    /\ now \in 0..MaxTime
    /\ metadata \in [Keys -> (0..MaxTime) \cup {None}]
    /\ received \in [Events -> Keys \cup {None}]
    /\ admitted \in [Events -> 0..MaxTime]
    /\ results \in [Events -> Keys \cup {None}]
    /\ published \in [Events -> Keys \cup {None}]
    /\ dead \subseteq StoredEvents /\ up \in BOOLEAN
CaptureTruth == \A e \in Events : results[e] # None => results[e] = Truth[e]
StableResults == \A e \in Events : published[e] # None => results[e] = published[e]
TerminalExpiry == \A e \in dead : results[e] = None
Useful == \A e \in StoredEvents \ dead :
    metadata[received[e]] # None => results[e] # None
Capacity == Cardinality(StoredKeys) <= MaxMetadata /\ Cardinality(StoredEvents) <= MaxEvents
Expiry == /\ \A k \in StoredKeys : ~Due(metadata[k], MetadataTTL)
          /\ \A e \in StoredEvents : results[e] = None =>
                  (e \in dead) = Due(admitted[e], PendingTTL)
=============================================================================
