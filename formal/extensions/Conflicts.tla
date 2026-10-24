----------------------------- MODULE Conflicts -----------------------------
EXTENDS Naturals
CONSTANTS EarlyConflict, MetadataConflict, None

Keys == {"A", "B"}
Events == {"E1", "E2"}
Workloads == {"WA", "WB"}
Data == [key : Keys, workload : Workloads]
Truth == [e \in Events |-> IF e = "E1" THEN "A" ELSE "B"]
World == [k \in Keys |-> IF k = "A" THEN "WA" ELSE "WB"]

VARIABLES metadata, received, results, firstKey, published
vars == <<metadata, received, results, firstKey, published>>
Init ==
    /\ metadata = [k \in Keys |-> None]
    /\ received = [e \in Events |-> None]
    /\ results = [e \in Events |-> None]
    /\ firstKey = [e \in Events |-> None]
    /\ published = [e \in Events |-> None]

\* Any key can be replayed once the ID has been delivered. EarlyConflict also
\* permits the very first payload to lie. firstKey/published are audit ledgers.
Activity(e, k) ==
    /\ EarlyConflict \/ received[e] # None \/ k = Truth[e]
    /\ LET first == received[e] = None
           answer == IF metadata[k] = None THEN None
                     ELSE [key |-> k, workload |-> metadata[k]]
       IN /\ received' = IF first THEN [received EXCEPT ![e] = k] ELSE received
          /\ results' = IF first THEN [results EXCEPT ![e] = answer] ELSE results
          /\ firstKey' = IF firstKey[e] = None
                         THEN [firstKey EXCEPT ![e] = k] ELSE firstKey
          /\ published' = IF first /\ answer # None
                          THEN [published EXCEPT ![e] = answer] ELSE published
    /\ UNCHANGED metadata

\* This matches Go's overwrite semantics, including resolving only pending
\* events. It intentionally does not impose immutable metadata when enabled.
Meta(k, w) ==
    /\ w = World[k] \/ (MetadataConflict /\ metadata[k] # None)
    /\ metadata' = [metadata EXCEPT ![k] = w]
    /\ results' = [e \in Events |->
           IF received[e] = k /\ results[e] = None
           THEN [key |-> k, workload |-> w] ELSE results[e]]
    /\ published' = [e \in Events |->
           IF firstKey[e] = k /\ published[e] = None
           THEN [key |-> k, workload |-> w] ELSE published[e]]
    /\ UNCHANGED <<received, firstKey>>

Next == (\E e \in Events, k \in Keys : Activity(e, k))
        \/ (\E k \in Keys, w \in Workloads : Meta(k, w))
Spec == Init /\ [][Next]_vars
TypeOK ==
    /\ metadata \in [Keys -> Workloads \cup {None}]
    /\ received \in [Events -> Keys \cup {None}]
    /\ firstKey \in [Events -> Keys \cup {None}]
    /\ results \in [Events -> Data \cup {None}]
    /\ published \in [Events -> Data \cup {None}]
FirstOrigin == received = firstKey
StableResults == \A e \in Events : published[e] # None => results[e] = published[e]
Useful == \A e \in Events :
    IF received[e] = None THEN TRUE
    ELSE metadata[received[e]] # None => results[e] # None
CaptureTruth == \A e \in Events : results[e] # None =>
    results[e] = [key |-> Truth[e], workload |-> World[Truth[e]]]
=============================================================================
