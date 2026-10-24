------------------------------ MODULE Storage ------------------------------
EXTENDS Naturals, FiniteSets
CONSTANTS Bounded, CrashMode, MaxMetadata, MaxEvents, None
Keys == {"A", "B"}
Events == {"E1", "E2"}
Workloads == {"WA", "WB"}
Data == [key : Keys, workload : Workloads]
Truth == [e \in Events |-> IF e = "E1" THEN "A" ELSE "B"]
World == [k \in Keys |-> IF k = "A" THEN "WA" ELSE "WB"]
Reply(kind, id, accepted) ==
    [kind |-> kind, id |-> id, outcome |-> IF accepted THEN "accepted" ELSE "rejected"]

VARIABLES up, metadata, received, results, everAccepted, published, reply
vars == <<up, metadata, received, results, everAccepted, published, reply>>
StoredKeys == {k \in Keys : metadata[k] # None}
StoredEvents == {e \in Events : received[e] # None}
Init ==
    /\ up = TRUE
    /\ metadata = [k \in Keys |-> None]
    /\ received = [e \in Events |-> None]
    /\ results = [e \in Events |-> None]
    /\ everAccepted = {}
    /\ published = [e \in Events |-> None]
    /\ reply = None

\* Admission and acknowledgement form one atomic transaction. A full table
\* rejects new identities, but permits duplicates. No accepted entry is evicted.
Activity(e, k) ==
    /\ up
    /\ LET accept == ~Bounded \/ e \in StoredEvents \/ Cardinality(StoredEvents) < MaxEvents
           answer == IF metadata[k] = None THEN None
                     ELSE [key |-> k, workload |-> metadata[k]]
       IN /\ received' = IF accept /\ e \notin StoredEvents
                         THEN [received EXCEPT ![e] = k] ELSE received
          /\ results' = IF accept /\ e \notin StoredEvents
                        THEN [results EXCEPT ![e] = answer] ELSE results
          /\ everAccepted' = IF accept THEN everAccepted \cup {e} ELSE everAccepted
          /\ published' = IF accept /\ published[e] = None /\ answer # None
                          THEN [published EXCEPT ![e] = answer] ELSE published
          /\ reply' = Reply("activity", e, accept)
    /\ UNCHANGED <<up, metadata>>

Meta(k, w) ==
    /\ up
    /\ LET accept == ~Bounded \/ k \in StoredKeys \/ Cardinality(StoredKeys) < MaxMetadata
           answer == [key |-> k, workload |-> w]
       IN /\ metadata' = IF accept THEN [metadata EXCEPT ![k] = w] ELSE metadata
          /\ results' = [e \in Events |->
                 IF accept /\ received[e] = k /\ results[e] = None
                 THEN answer ELSE results[e]]
          /\ published' = [e \in Events |->
                 IF accept /\ received[e] = k /\ published[e] = None
                 THEN answer ELSE published[e]]
          /\ reply' = Reply("metadata", k, accept)
    /\ UNCHANGED <<up, received, everAccepted>>

\* Durable mode abstracts atomic committed storage, not a WAL implementation.
\* A crash between actions either precedes or follows a complete transaction.
Crash ==
    /\ CrashMode # "none" /\ up
    /\ up' = FALSE
    /\ metadata' = IF CrashMode = "volatile" THEN [k \in Keys |-> None] ELSE metadata
    /\ received' = IF CrashMode = "volatile" THEN [e \in Events |-> None] ELSE received
    /\ results' = IF CrashMode = "volatile" THEN [e \in Events |-> None] ELSE results
    /\ reply' = None
    /\ UNCHANGED <<everAccepted, published>>
Restart ==
    /\ ~up /\ up' = TRUE
    /\ UNCHANGED <<metadata, received, results, everAccepted, published, reply>>

\* Environment emits truthful messages. Delivery actions use only their
\* explicit inputs and backend state, never Truth or World to resolve events.
Next == (\E e \in Events : Activity(e, Truth[e]))
        \/ (\E k \in Keys : Meta(k, World[k]))
        \/ Crash \/ Restart
Spec == Init /\ [][Next]_vars
TypeOK ==
    /\ up \in BOOLEAN
    /\ metadata \in [Keys -> Workloads \cup {None}]
    /\ received \in [Events -> Keys \cup {None}]
    /\ everAccepted \subseteq Events
    /\ results \in [Events -> Data \cup {None}]
    /\ published \in [Events -> Data \cup {None}]
    /\ reply \in {None} \cup
           {Reply("activity", e, a) : e \in Events, a \in BOOLEAN} \cup
           {Reply("metadata", k, a) : k \in Keys, a \in BOOLEAN}
Capacity == Cardinality(StoredKeys) <= MaxMetadata /\ Cardinality(StoredEvents) <= MaxEvents
AcceptedRetained == everAccepted \subseteq StoredEvents
StableResults == \A e \in Events : published[e] # None => results[e] = published[e]
CaptureTruth == \A e \in Events : results[e] # None =>
    results[e] = [key |-> Truth[e], workload |-> World[Truth[e]]]
Useful == \A e \in StoredEvents : metadata[received[e]] # None => results[e] # None
ReplyConsistent ==
    IF reply = None THEN TRUE
    ELSE (reply.outcome = "accepted") =
         (IF reply.kind = "activity" THEN reply.id \in StoredEvents
          ELSE reply.id \in StoredKeys)
=============================================================================
